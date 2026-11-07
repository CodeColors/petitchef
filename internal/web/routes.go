package web

import "net/http"

// routes associe chaque route réservée aux utilisateurs connectés à son
// handler ; les routes POST déclenchent un examen des alertes.
func (s *Server) routes() map[string]http.HandlerFunc {
	return map[string]http.HandlerFunc{
		"GET /{$}":                                    s.home,
		"POST /preferences/theme":                     s.setTheme,
		"GET /alertes":                                s.alertsPage,
		"POST /alertes/acquitter":                     s.acknowledgeAlert,
		"GET /alertes/reglages":                       s.alertSettingsPage,
		"POST /alertes/reglages":                      s.saveAlertSettings,
		"POST /alertes/canaux":                        s.saveChannels,
		"POST /alertes/essai":                         s.testChannels,
		"GET /sauvegardes":                            s.backupsPage,
		"POST /sauvegardes":                           s.backupNow,
		"POST /sauvegardes/reglages":                  s.saveBackupSettings,
		"POST /sauvegardes/restaurer":                 s.restoreBackup,
		"GET /sauvegardes/fichiers/{name}":            s.downloadBackup,
		"POST /sauvegardes/fichiers/{name}/supprimer": s.deleteBackup,
		"GET /parametres":                             s.settingsPage,
		"GET /utilisateurs":                           s.usersPage,
		"POST /utilisateurs":                          s.createUser,
		"POST /utilisateurs/mot-de-passe":             s.changePassword,
		"POST /utilisateurs/{id}/supprimer":           s.deleteUser,
		"GET /plus":                                   s.morePage,
		"GET /catalogue":                              s.catalogPage,
		"GET /catalogue/suggestions":                  s.productSuggestions,
		"GET /catalogue/nouveau":                      s.newProductPage,
		"POST /catalogue":                             s.saveProduct,
		"GET /catalogue/{id}":                         s.editProductPage,
		"POST /catalogue/{id}":                        s.saveProduct,
		"POST /catalogue/{id}/retirer":                s.archiveProduct,
		"GET /magasins":                               s.storesPage,
		"POST /magasins":                              s.saveStore,
		"POST /magasins/{id}/supprimer":               s.deleteStore,
		"POST /magasins/{id}/rayons":                  s.moveAisle,
		"GET /emplacements":                           s.locationsPage,
		"POST /emplacements":                          s.saveLocation,
		"POST /emplacements/{id}/supprimer":           s.deleteLocation,
	}
}

// publicRoutes déclare les routes accessibles sans session (flux iCal à
// jeton secret, service worker).
func (s *Server) publicRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /sw.js", s.serviceWorker)
}

func (s *Server) morePage(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "plus", "Plus", nil)
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, http.StatusOK, "home", "Accueil", nil)
}

// serviceWorker sert le service worker à la racine (portée « / » requise
// pour intercepter l'écran de la liste de courses) ; il ne met en cache que
// cet écran. Voir docs/DECISIONS.md.
func (s *Server) serviceWorker(w http.ResponseWriter, r *http.Request) {
	f := s.assets["sw.js"]
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Service-Worker-Allowed", "/")
	_, _ = w.Write(f.body)
}
