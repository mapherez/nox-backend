package main

import (
	"github.com/mapherez/nox-backend/backend/internal/command"
	"log"
)

// buildVersion is supplied by release builds using -ldflags -X main.buildVersion.
var buildVersion string

func main() {
	if err := command.Run(buildVersion); err != nil {
		log.Fatal(err)
	}
}
