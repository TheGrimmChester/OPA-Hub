package main

import (
	"log"

	"github.com/TheGrimmChester/opa-hub/internal/config"
	"github.com/TheGrimmChester/opa-hub/internal/server"
)

func main() {
	cfg := config.Load()
	srv := server.New(cfg)
	log.Fatal(srv.ListenAndServe())
}
