// Commande petitchef : serveur web de gestion de la cuisine, de l'inventaire
// et des courses du foyer.
//
// Usage :
//
//	petitchef                               démarre le serveur
//	petitchef healthcheck                   interroge /healthz (HEALTHCHECK Docker, image sans shell)
//	petitchef reset-password <identifiant>  attribue un mot de passe aléatoire (oubli)
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata" // base des fuseaux embarquée : l'image scratch n'en a pas (TZ)

	// Certificats racines embarqués, utilisés faute de magasin système (image
	// scratch) : notifications SMTP chiffrées, webhook et copie WebDAV en
	// HTTPS.
	_ "golang.org/x/crypto/x509roots/fallback"

	"github.com/codecolors/petitchef/internal/alerts"
	"github.com/codecolors/petitchef/internal/auth"
	"github.com/codecolors/petitchef/internal/backup"
	"github.com/codecolors/petitchef/internal/catalog"
	"github.com/codecolors/petitchef/internal/config"
	"github.com/codecolors/petitchef/internal/platform/calendar"
	"github.com/codecolors/petitchef/internal/platform/db"
	"github.com/codecolors/petitchef/internal/scheduler"
	"github.com/codecolors/petitchef/internal/stock"
	"github.com/codecolors/petitchef/internal/web"
)

// version est renseignée à la compilation (-ldflags "-X main.version=…").
var version = "dev"

func main() {
	if len(os.Args) > 1 {
		var err error
		switch os.Args[1] {
		case "healthcheck":
			err = healthcheck()
		case "reset-password":
			err = resetPassword(os.Args[2:])
		default:
			err = fmt.Errorf("commande inconnue %q : healthcheck ou reset-password <identifiant>", os.Args[1])
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("arrêt sur erreur", "err", err)
		os.Exit(1)
	}
}

func run() error {
	start := time.Now()
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("dossier de données : %w", err)
	}
	// Une restauration préparée avant le redémarrage s'applique avant
	// l'ouverture de la base.
	if restored, err := backup.ApplyPending(cfg.DataDir, time.Now()); err != nil {
		return fmt.Errorf("restauration de la sauvegarde : %w", err)
	} else if restored {
		slog.Info("sauvegarde restaurée ; les données remplacées sont dans avant-restauration")
	}
	database, err := db.Open(ctx, cfg.DBPath())
	if err != nil {
		return err
	}
	defer func() {
		if err := database.Close(); err != nil {
			slog.Error("fermeture de la base", "err", err)
		}
	}()

	if err := catalog.SyncSeed(ctx, database); err != nil {
		return err
	}
	products := catalog.NewService(database)
	inventory := stock.NewService(database, products)
	baseURL := ""
	if cfg.BaseURL != nil {
		baseURL = cfg.BaseURL.String()
	}
	alertCenter := alerts.NewService(database, baseURL)
	backups := backup.NewService(database, cfg.DataDir, cfg.BackupPassphrase)
	handler, err := web.New(cfg, database, web.Services{
		Auth:    auth.NewService(database),
		Catalog: products,
		Stock:   inventory,
		Alerts:  alertCenter,
		Backups: backups,
		// Après une restauration préparée : arrêt propre, la politique de
		// redémarrage du conteneur relance PetitChef.
		Restart: stop,
	})
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", cfg.Addr())
	if err != nil {
		return err
	}
	slog.Info("PetitChef démarré", "version", version, "adresse", cfg.Addr(), "fuseau", time.Local.String(),
		"démarrage", time.Since(start).Round(time.Millisecond))

	// Travaux de fond : examen des alertes à la demande, travaux quotidiens
	// (alertes, sauvegarde). Arrêtés, et attendus, avant la fermeture de
	// la base.
	var background sync.WaitGroup
	background.Go(func() { alertCenter.Run(ctx) })
	background.Go(func() {
		scheduler.Run(ctx,
			scheduler.Job{Name: "estimation des consommations", Run: func(ctx context.Context, today calendar.Date) error {
				_, err := inventory.RefreshEstimates(ctx, today)
				return err
			}},
			scheduler.Job{Name: "lots épuisés", Run: func(ctx context.Context, today calendar.Date) error {
				_, err := inventory.Purge(ctx, today)
				return err
			}},
			scheduler.Job{Name: "alertes", Run: alertCenter.Daily},
			scheduler.Job{Name: "sauvegarde automatique", Run: func(ctx context.Context, _ calendar.Date) error {
				return backups.Daily(ctx, time.Now())
			}},
		)
	})
	defer func() {
		stop()
		background.Wait()
	}()

	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	slog.Info("arrêt demandé")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// resetPassword attribue un mot de passe aléatoire à un utilisateur et
// l'affiche ; PetitChef peut tourner en même temps (SQLite en mode WAL).
func resetPassword(args []string) error {
	if len(args) != 1 {
		return errors.New("usage : petitchef reset-password <identifiant>")
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	ctx := context.Background()
	database, err := db.Open(ctx, cfg.DBPath())
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()
	password, err := auth.NewService(database).ResetPassword(ctx, args[0])
	if errors.Is(err, auth.ErrNotFound) {
		return fmt.Errorf("aucun utilisateur %q", args[0])
	}
	if err != nil {
		return err
	}
	fmt.Printf("Nouveau mot de passe de %s : %s\nChangez-le après connexion (Plus > Paramètres > Utilisateurs).\n", args[0], password)
	return nil
}

// healthcheck renvoie une erreur si le serveur local ne répond pas « ok ».
func healthcheck() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1"+cfg.Addr()+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("healthz : statut %d", resp.StatusCode)
	}
	return nil
}
