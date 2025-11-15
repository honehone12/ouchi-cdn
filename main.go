package main

import (
	"flag"
	"fmt"
	"net/url"
	"ouchi/ttlcache"
	"path"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/labstack/gommon/log"
)

func main() {
	configPath := flag.String("config", "config.json", "configuration json file")
	flag.Parse()

	e := echo.New()
	e.Use(middleware.Logger())
	e.Logger.SetLevel(log.INFO)
	e.Logger.SetPrefix("OUCHI-CDN")

	config, err := ttlcache.ReadConfigFile(path.Clean(*configPath))
	if err != nil {
		e.Logger.Fatal(err)
	}

	cache := ttlcache.NewTtlCache(ttlcache.TtlCacheConfig{
		Ttl:     time.Second * config.TtlSec,
		Tick:    time.Second * config.TickSec,
		Headers: config.Headers,
		Logger:  e.Logger,
	})
	if err != nil {
		e.Logger.Fatal(err)
	}

	origin, err := url.Parse(fmt.Sprintf("http://localhost:%d", config.OriginPort))
	if err != nil {
		e.Logger.Fatal(err)
	}
	originBalancer := middleware.NewRoundRobinBalancer(
		[]*middleware.ProxyTarget{
			{
				Name: "origin",
				URL:  origin,
			},
		},
	)

	originGroup := e.Group("/*")
	originGroup.Use(cache.Middleware())
	originGroup.Use(middleware.Proxy(originBalancer))

	if err := e.Start(fmt.Sprintf("0.0.0.0:%d", config.ListenPort)); err != nil {
		e.Logger.Error(err)
	}
}
