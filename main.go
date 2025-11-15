package main

import (
	"flag"
	"fmt"
	"net/url"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/labstack/gommon/log"
)

func main() {
	originPort := flag.Uint("proxy", 8083, "origin port")
	listenPort := flag.Uint("listen", 8084, "listen port")
	flag.Parse()

	e := echo.New()
	e.Use(middleware.Logger())
	e.Logger.SetLevel(log.INFO)
	e.Logger.SetPrefix("OUCHI-CDN")

	origin, err := url.Parse(fmt.Sprintf("http://localhost:%d", *originPort))
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
	originGroup.Use(middleware.Proxy(originBalancer))

	if err := e.Start(fmt.Sprintf("0.0.0.0:%d", *listenPort)); err != nil {
		e.Logger.Error(err)
	}
}
