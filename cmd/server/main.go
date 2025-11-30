package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/BaikalMine/em-subscription-service/internal/config"
	"github.com/BaikalMine/em-subscription-service/internal/handlers"
	"github.com/BaikalMine/em-subscription-service/internal/storage"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	_ "github.com/lib/pq"
	"github.com/sirupsen/logrus"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(fmt.Errorf("load config: %w", err))
	}

	logger := logrus.New()
	level, err := logrus.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = logrus.InfoLevel
	}
	logger.SetLevel(level)
	logger.SetFormatter(&logrus.TextFormatter{FullTimestamp: true})

	db, err := sql.Open("postgres", cfg.DSN())
	if err != nil {
		logger.WithError(err).Fatal("failed to connect to database")
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		logger.WithError(err).Fatal("failed to ping database")
	}

	store := storage.NewStore(db)
	subHandlers := handlers.NewHandler(store, logger)

	// router создаётся, подключаются middleware и маршруты.
	router := chi.NewRouter()
	router.Use(middleware.RequestID)
	router.Use(middleware.RealIP)
	router.Use(requestLogger(logger))
	router.Use(middleware.Timeout(60 * time.Second))

	subHandlers.RegisterRoutes(router)

	router.Get("/swagger.yaml", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "docs/swagger.yaml")
	})
	router.Handle("/docs/*", http.StripPrefix("/docs/", http.FileServer(http.Dir("docs"))))

	server := &http.Server{
		Addr:    fmt.Sprintf(":%s", cfg.ServerPort),
		Handler: router,
	}

	logger.WithField("addr", server.Addr).Info("subscription service starting")

	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.WithError(err).Fatal("server stopped unexpectedly")
		}
	}()

	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	<-signalCtx.Done()
	logger.Info("shutting down subscription service")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.WithError(err).Error("graceful shutdown failed")
	}
}

func requestLogger(logger *logrus.Logger) func(http.Handler) http.Handler {
	// Встраиваем логирование для каждого запроса.
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			logger.WithFields(logrus.Fields{
				"method":      r.Method,
				"path":        r.URL.Path,
				"status":      ww.Status(),
				"duration_ms": time.Since(start).Milliseconds(),
				"request_id":  middleware.GetReqID(r.Context()),
			}).Info("request served")
		})
	}

}
