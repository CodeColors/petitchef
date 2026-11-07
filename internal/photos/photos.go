// Package photos stocke les photos des recettes et des produits : réduites
// à 1600 px de large et recompressées en JPEG côté serveur, rangées dans
// DATA_DIR/photos, dans la limite d'un quota.
package photos

import (
	"crypto/rand"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sync"

	"github.com/codecolors/petitchef/internal/platform/validation"
)

// ErrNotFound signale une photo inexistante.
var ErrNotFound = errors.New("photo introuvable")

var nameRe = regexp.MustCompile(`^[A-Za-z0-9]{26}\.jpg$`)

// Service gère les fichiers photo.
type Service struct {
	dir       string
	maxUpload int64
	quota     int64
	mu        sync.Mutex // une réduction à la fois : borne la mémoire en pic
}

// NewService crée le service des photos.
func NewService(dir string, maxUpload, quota int64) *Service {
	return &Service{dir: dir, maxUpload: maxUpload, quota: quota}
}

// Save lit une image envoyée, la réduit et l'enregistre ; renvoie le nom
// du fichier.
func (s *Service) Save(r io.Reader) (string, error) {
	data, err := io.ReadAll(io.LimitReader(r, s.maxUpload+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > s.maxUpload {
		return "", validation.Errorf("Photo trop lourde : %d Mo au plus.", s.maxUpload>>20)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	small, err := shrink(data)
	data = nil //nolint:ineffassign // libère l'original avant la fin de la fonction
	// Le décodage d'une photo de 12 Mpx occupe plusieurs dizaines de Mo :
	// ils sont rendus au système aussitôt.
	defer debug.FreeOSMemory()
	if err != nil {
		return "", err
	}
	used, err := s.Used()
	if err != nil {
		return "", err
	}
	if used+int64(len(small)) > s.quota {
		return "", validation.Error("Quota de photos atteint : supprimez des photos ou augmentez PHOTOS_QUOTA_MB.")
	}
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return "", err
	}
	name := rand.Text() + ".jpg"
	if err := os.WriteFile(filepath.Join(s.dir, name), small, 0o600); err != nil {
		return "", err
	}
	return name, nil
}

// Path renvoie le chemin d'une photo, après contrôle de son nom.
func (s *Service) Path(name string) (string, error) {
	if !nameRe.MatchString(name) {
		return "", ErrNotFound
	}
	p := filepath.Join(s.dir, name)
	if _, err := os.Stat(p); err != nil {
		return "", ErrNotFound
	}
	return p, nil
}

// Delete supprime une photo (sans erreur si elle n'existe pas).
func (s *Service) Delete(name string) error {
	if name == "" || !nameRe.MatchString(name) {
		return nil
	}
	err := os.Remove(filepath.Join(s.dir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Used renvoie la place occupée par les photos.
func (s *Service) Used() (int64, error) {
	entries, err := os.ReadDir(s.dir)
	if errors.Is(err, fs.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var n int64
	for _, e := range entries {
		if info, err := e.Info(); err == nil {
			n += info.Size()
		}
	}
	return n, nil
}
