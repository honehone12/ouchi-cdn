package main

import (
	"flag"
	"fmt"
	"ouchi/memory"
	"ouchi/ttlcache"
	"path"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/labstack/gommon/log"
)

func main() {
	debug := flag.Bool("debug", false, "use debug mode")
	configPath := flag.String("config", "config.json", "configuration json file")
	flag.Parse()

	e := echo.New()
	e.Use(middleware.Logger())
	if *debug {
		e.Logger.SetLevel(log.DEBUG)
	} else {
		e.Logger.SetLevel(log.INFO)
	}
	e.Logger.SetPrefix("OUCH-CACHE")

	config, err := ttlcache.ReadConfigFile(path.Clean(*configPath))
	if err != nil {
		e.Logger.Fatal(err)
	}

	cache, err := memory.NewMemoryTtlCache(ttlcache.TtlCacheConfig{
		ProxyUrl: fmt.Sprintf("http://localhost:%d", config.OriginPort),
		TtlSec:   time.Second * config.TtlSec,
		TickSec:  time.Second * config.TickSec,
		Headers:  config.Headers,
		Logger:   e.Logger,
	})

	originGroup := e.Group("/*")
	originGroup.Use(cache.Middleware())

	if err := e.Start(fmt.Sprintf("0.0.0.0:%d", config.ListenPort)); err != nil {
		e.Logger.Error(err)
	}
}
