// fixture-redis serves the JSON fixtures of fixtures/test and fixtures/rack on a redis port, for container
// tests of the exporter without a switch: fixture-redis -addr 127.0.0.1:6379 fixtures/rack/*.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"

	"github.com/alicebob/miniredis/v2"
)

type database struct {
	ID   string                       `json:"id"`
	Data map[string]map[string]string `json:"data"`
}

var dbIDs = map[string]int{"APPL_DB": 0, "COUNTERS_DB": 2, "CONFIG_DB": 4, "STATE_DB": 6}

func main() {
	addr := flag.String("addr", "127.0.0.1:6379", "listen address")
	flag.Parse()

	m := miniredis.NewMiniRedis()
	if err := m.StartAddr(*addr); err != nil {
		log.Fatal(err)
	}

	defer m.Close()

	for _, file := range flag.Args() {
		raw, err := os.ReadFile(file)
		if err != nil {
			log.Fatal(err)
		}

		var db database
		if err := json.Unmarshal(raw, &db); err != nil {
			log.Fatalf("%s: %v", file, err)
		}

		id, ok := dbIDs[db.ID]
		if !ok {
			log.Fatalf("%s: unknown db %q", file, db.ID)
		}

		m.Select(id)

		for key, hash := range db.Data {
			for field, value := range hash {
				m.HSet(key, field, value)
			}
		}

		fmt.Println("loaded", file, "db", strconv.Itoa(id), len(db.Data), "keys")
	}

	fmt.Println("serving on", *addr)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop
}
