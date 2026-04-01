package main

import (
	"log"
	"net/http"

	"poker/auth"
	"poker/game"
	"poker/server"
)

func main() {
	store, err := auth.NewStore("../data/game.db")
	if err != nil {
		log.Fatal("failed to init auth store:", err)
	}

	manager := game.NewManager()
	hub := server.NewHub(manager, store)
	srv := server.NewServer(manager, hub, store)

	addr := ":8080"
	log.Println("Texas Hold'em server listening on", addr)
	if err := http.ListenAndServe(addr, srv.Routes()); err != nil {
		log.Fatal(err)
	}
}
