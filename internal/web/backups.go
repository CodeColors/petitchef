package web

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/codecolors/petitchef/internal/backup"
	"github.com/codecolors/petitchef/internal/platform/validation"
)

// maxRestoreUpload borne une archive déposée pour restauration ; elle est
// lue au fil de l'envoi, sans copie en mémoire.
const maxRestoreUpload = 8 << 30

// backupsView est la donnée de la page des sauvegardes.
type backupsView struct {
	Archives  []backup.Archive
	Settings  backup.Settings
	Encrypted bool
	Pending   bool
	LastAt    time.Time
	Error     string
}

// fileSize écrit une taille de fichier : « 820 Ko », « 12,4 Mo ».
func fileSize(n int64) string {
	switch {
	case n < 1<<20:
		return strconv.FormatInt(max((n+512)>>10, 1), 10) + " Ko"
	case n < 1<<30:
		return strconv.FormatFloat(float64(n)/(1<<20), 'f', 1, 64) + " Mo"
	}
	return strconv.FormatFloat(float64(n)/(1<<30), 'f', 2, 64) + " Go"
}

func (s *Server) backupsPage(w http.ResponseWriter, r *http.Request) {
	s.renderBackups(w, r, http.StatusOK, nil, "")
}

// renderBackups affiche la page ; form, s'il est donné, est la saisie
// refusée des réglages, réaffichée telle quelle avec le message msg.
func (s *Server) renderBackups(w http.ResponseWriter, r *http.Request, status int, form *backup.Settings, msg string) {
	v := backupsView{Encrypted: s.Backups.Encrypted(), Pending: s.Backups.Pending(), Error: msg}
	var err error
	if v.Settings, err = s.Backups.Settings(r.Context()); err != nil {
		s.serverError(w, r, err)
		return
	}
	if v.LastAt, err = time.Parse(time.RFC3339, v.Settings.LastAt); err != nil {
		v.LastAt = time.Time{}
	}
	if form != nil {
		form.LastAt, form.LastError = v.Settings.LastAt, v.Settings.LastError
		v.Settings = *form
	}
	if v.Archives, err = s.Backups.List(); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.render(w, r, status, "backups", "Sauvegardes", v)
}

// backupNow fait une sauvegarde complète (copie externe comprise).
func (s *Server) backupNow(w http.ResponseWriter, r *http.Request) {
	allowLongTransfer(w)
	st, err := s.Backups.Settings(r.Context())
	if err == nil {
		_, err = s.Backups.Run(r.Context(), st, false, time.Now())
	}
	if err != nil {
		if _, ok := validation.Message(err); ok {
			s.failed(w, r, err, "/sauvegardes")
			return
		}
		s.redirect(w, r, "/sauvegardes", "Échec de la sauvegarde : "+err.Error())
		return
	}
	s.redirect(w, r, "/sauvegardes", "Sauvegarde terminée.")
}

func (s *Server) saveBackupSettings(w http.ResponseWriter, r *http.Request) {
	if parseForm(w, r) != nil {
		s.renderError(w, r, http.StatusBadRequest, "Formulaire illisible.")
		return
	}
	keep, _ := strconv.Atoi(r.PostFormValue("keep"))
	st := backup.Settings{
		Auto: r.PostFormValue("auto") != "", Keep: keep, Copy: backup.Copy(r.PostFormValue("copy")), CopyDir: r.PostFormValue("copy_dir"),
		WebDAVURL: r.PostFormValue("webdav_url"), WebDAVUser: r.PostFormValue("webdav_user"), WebDAVPassword: r.PostFormValue("webdav_password"),
	}
	if err := s.Backups.SaveSettings(r.Context(), st); err != nil {
		if msg, ok := s.inputError(w, r, err); ok {
			st.WebDAVPassword = ""
			s.renderBackups(w, r, http.StatusUnprocessableEntity, &st, msg)
		}
		return
	}
	s.redirect(w, r, "/sauvegardes", "Réglages des sauvegardes enregistrés.")
}

func (s *Server) downloadBackup(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	f, err := s.Backups.Open(name)
	if err != nil {
		s.inputError(w, r, err)
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	allowLongTransfer(w)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	http.ServeContent(w, r, name, info.ModTime(), f)
}

func (s *Server) deleteBackup(w http.ResponseWriter, r *http.Request) {
	if err := s.Backups.Delete(r.PathValue("name")); err != nil {
		s.failed(w, r, err, "/sauvegardes")
		return
	}
	s.redirect(w, r, "/sauvegardes", "Sauvegarde supprimée.")
}

// restoreBackup prépare la restauration d'une archive conservée (?nom=) ou
// déposée (champ fichier), puis redémarre PetitChef, qui l'applique avant
// d'ouvrir la base.
func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) {
	allowLongTransfer(w)
	var err error
	if name := r.URL.Query().Get("nom"); name != "" {
		err = s.Backups.StageArchive(r.Context(), name)
	} else {
		err = s.stageUpload(w, r)
	}
	if err != nil {
		if errors.Is(err, backup.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.failed(w, r, err, "/sauvegardes")
		return
	}
	// La page se recharge d'elle-même une fois PetitChef relancé.
	w.Header().Set("Refresh", "8; url=/")
	s.render(w, r, http.StatusOK, "restarting", "Restauration", nil)
	s.Restart()
}

// stageUpload lit l'archive déposée au fil de l'envoi.
func (s *Server) stageUpload(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRestoreUpload)
	mr, err := r.MultipartReader()
	if err != nil {
		return validation.Error("Choisissez le fichier de sauvegarde.")
	}
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return validation.Error("Choisissez le fichier de sauvegarde.")
		}
		if err != nil {
			return validation.Error("Envoi interrompu ou fichier trop volumineux.")
		}
		if part.FormName() == "fichier" && part.FileName() != "" {
			return s.Backups.Stage(r.Context(), part)
		}
	}
}
