package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"bell_server/internal/api"
	"bell_server/internal/database"
	"bell_server/internal/scheduler"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	log.Println("==================================================")
	log.Println("     BELL PINTAR - SERVER ENGINE (GO DAEMON)      ")
	log.Println("==================================================")

	// 1. Initialize SQLite Database
	dbPath := os.Getenv("BELL_DB_PATH")
	if dbPath == "" {
		dbPath = database.GetDefaultDBPath()
	}

	_, err := database.InitDB(dbPath)
	if err != nil {
		log.Fatalf("[FATAL] Database connection failed: %v", err)
	}

	// 2. Start Background Scheduler Loop
	scheduler.GlobalScheduler.Start()

	// 3. Setup Gin HTTP Engine
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	// CORS Configuration (Allow all origins for local school LAN & Flutter mobile/web clients)
	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"*"},
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Accept", "Authorization", "X-Device-Id", "X-Device-Name", "X-Device-Platform"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// 4. Setup API Routes
	api.SetupRoutes(r)

	// 5. Run HTTP Server
	port := database.GetSetting("api_port", "8088")
	addr := fmt.Sprintf("0.0.0.0:%s", port)

	srv := &http.Server{
		Addr:    addr,
		Handler: r,
	}

	go func() {
		log.Printf("[SERVER] Bell Pintar API listening on http://%s", addr)
		log.Printf("[SERVER] Health check available at: http://localhost:%s/api/v1/ping", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("[FATAL] Server startup failed: %v", err)
		}
	}()

	// 6. Graceful Shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("[SERVER] Shutting down Bell Pintar Server gracefully...")
	scheduler.GlobalScheduler.Stop()
	if database.DB != nil {
		_ = database.DB.Close()
	}
	log.Println("[SERVER] Server exited cleanly.")
}
