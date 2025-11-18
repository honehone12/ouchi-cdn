package main

import (
	"encoding/json"
	"log"
	"os"
	"ouchi/ttlcache"
)

func main() {
	config := ttlcache.ConfigFile{
		Headers: map[string]string{
			"Cache-Control": "max-age=900",
		},
		TtlSec:     900,
		TickSec:    60,
		OriginPort: 8082,
		ListenPort: 8083,
	}

	b, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		log.Fatalln(err)
	}

	if err := os.WriteFile("config.json", b, 0660); err != nil {
		log.Fatalln(err)
	}

	log.Println("done")
}
