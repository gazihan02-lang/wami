package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"wami/scheduler"
	"wami/store"
	"wami/wa"
	"wami/web"
)

func main() {
	db, err := store.New("bot.db")
	if err != nil {
		log.Fatalf("store: %v", err)
	}
	defer db.Close()

	waClient, err := wa.New(db)
	if err != nil {
		log.Fatalf("wa: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go waClient.Start(ctx)

	sched := scheduler.New(db, waClient)
	go sched.Run(ctx)

	srv := web.NewServer(db, waClient)
	go func() {
		log.Println("Server başlatıldı → http://localhost:8081")
		if err := http.ListenAndServe(":8081", srv); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	log.Println("Kapatılıyor...")
}
