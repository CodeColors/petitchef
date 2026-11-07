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
		"GET /stock":                                  s.stockPage,
		"GET /stock/priorite":                         s.stockPage,
		"GET /stock/ajouter":                          s.addStockPage,
		"POST /stock/ajouter":                         s.addStock,
		"POST /stock/annuler":                         s.undoStock,
		"POST /stock/lots/{id}/{action}":              s.itemAction,
		"GET /stock/lots/{id}/deplacer":               s.moveItemPage,
		"POST /stock/lots/{id}/deplacer":              s.moveItem,
		"GET /stock/inventaire":                       s.inventoryPage,
		"POST /stock/inventaire":                      s.saveInventory,
		"GET /stock/produit/{id}":                     s.productStockPage,
		"POST /stock/produit/{id}/reglages":           s.saveStockSettings,
		"POST /stock/produit/{id}/presque-vide":       s.toggleLow,
		"GET /stock/suivi":                            s.watchedPage,
		"POST /stock/modeles/{slug}":                  s.applyStockTemplate,
		"GET /stock/scan":                             s.scanPage,
		"GET /stock/code":                             s.lookupBarcode,
		"GET /recettes":                               s.recipesPage,
		"GET /recettes/nouvelle":                      s.newRecipePage,
		"POST /recettes":                              s.saveRecipe,
		"GET /recettes/importer":                      s.importRecipesPage,
		"POST /recettes/importer":                     s.importRecipes,
		"GET /recettes/export.json":                   s.exportAllRecipes,
		"GET /recettes/{id}":                          s.recipePage,
		"GET /recettes/{id}/modifier":                 s.editRecipePage,
		"POST /recettes/{id}":                         s.saveRecipe,
		"POST /recettes/{id}/supprimer":               s.deleteRecipe,
		"POST /recettes/{id}/dupliquer":               s.duplicateRecipe,
		"POST /recettes/{id}/avis":                    s.rateRecipe,
		"POST /recettes/{id}/photo":                   s.uploadRecipePhoto,
		"GET /recettes/{id}/export/{format}":          s.exportRecipe,
		"GET /photos/{name}":                          s.servePhoto,
		"GET /equilibre":                              s.gridPage,
		"POST /equilibre":                             s.saveGrid,
		"GET /foyer":                                  s.householdPage,
		"POST /foyer":                                 s.saveMember,
		"POST /foyer/{id}/supprimer":                  s.deleteMember,
		"GET /menus":                                  s.menusPage,
		"GET /menus/imprimer":                         s.printMenu,
		"GET /menus/generer":                          s.generatePage,
		"POST /menus/generer":                         s.generateMenus,
		"GET /menus/repas/{id}":                       s.mealPage,
		"POST /menus/repas/{id}":                      s.updateMeal,
		"POST /menus/repas/{id}/plat":                 s.setDish,
		"POST /menus/plats/{id}/{action}":             s.dishAction,
		"GET /menus/ajouter":                          s.addToMenuPage,
		"POST /menus/ajouter":                         s.addToMenu,
		"GET /menus/modeles":                          s.modelsPage,
		"POST /menus/modeles":                         s.saveModel,
		"POST /menus/modeles/{id}/appliquer":          s.applyModel,
		"POST /menus/modeles/{id}/supprimer":          s.deleteModel,
		"GET /creneaux":                               s.slotsPage,
		"POST /creneaux/{id}":                         s.saveSlot,
		"POST /semaines-types":                        s.createTemplate,
		"POST /semaines-types/{id}":                   s.saveTemplate,
		"POST /semaines-types/{id}/supprimer":         s.deleteTemplate,
		"GET /ce-soir":                                s.tonightPage,
		"GET /courses":                                s.shoppingPage,
		"GET /courses/magasin":                        s.storeModePage,
		"POST /courses/recalculer":                    s.recomputeShopping,
		"POST /courses/ajouter":                       s.addShoppingItem,
		"POST /courses/lignes/{id}/cocher":            s.toggleItem,
		"POST /courses/lignes/{id}/retirer":           s.removeShoppingItem,
		"POST /courses/magasin":                       s.chooseStore,
		"POST /courses/rythme":                        s.saveSchedule,
		"GET /courses/texte":                          s.shoppingText,
		"GET /courses/ranger":                         s.putAwayPage,
		"POST /courses/ranger":                        s.putAway,
		"GET /menus/plats/{id}/cuisine":               s.cookPage,
		"POST /menus/plats/{id}/cuisine":              s.cook,
		"POST /menus/plats/{id}/decuisiner":           s.uncook,
		"GET /recettes/{id}/cuisine":                  s.cookingMode,
		"GET /statistiques":                           s.statsPage,
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
