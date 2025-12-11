package ttlcache

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"ouchi/cache"
	"ouchi/log"

	"github.com/labstack/echo/v4"
)

type TtlCache struct {
	store    cache.ChacheStore
	logger   log.Logger
	proxyUrl *url.URL
	proxy    *httputil.ReverseProxy

	headers map[string]string
}

func NewTtlCache(
	logger log.Logger,
	store cache.ChacheStore,
	proxyUrl string,
	headers map[string]string,
) (*TtlCache, error) {
	p, err := url.Parse(proxyUrl)
	if err != nil {
		return nil, err
	}
	proxy := httputil.NewSingleHostReverseProxy(p)

	c := &TtlCache{
		logger:   logger,
		store:    store,
		proxyUrl: p,
		proxy:    proxy,

		headers: headers,
	}

	// use modifier for reading and caching response
	c.proxy.ModifyResponse = c.onProxyResponse

	return c, nil
}

func (c *TtlCache) setConfiguredHeaders(header http.Header) {
	for k, v := range c.headers {
		header.Set(k, v)
	}
}

func (c *TtlCache) onProxyResponse(res *http.Response) error {
	if res.StatusCode == http.StatusOK {
		h := res.Header
		cacheControl := h.Get("Cache-Control")
		if cacheControl != "no-cache" && cacheControl != "no-store" {
			contentType := h.Get("Content-Type")
			b, err := io.ReadAll(res.Body)
			if err != nil {
				return err
			}

			if err := c.store.Set(
				res.Request.URL.RequestURI(),
				contentType,
				b,
			); err != nil {
				return err
			}

			res.Body = io.NopCloser(bytes.NewReader(b))
		}

		h.Set("X-Ouchi-Cache", "miss")
		c.setConfiguredHeaders(h)
	}

	return nil
}

func (c *TtlCache) middlewareHandler(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) error {
		req := ctx.Request()

		d, err := c.store.Get(req.URL.RequestURI())
		// cache miss - proxy the request
		if errors.Is(err, cache.ErrNoSuchKey) || errors.Is(err, cache.ErrExpired) {
			c.logger.Debug("not stored", err)
			req.Host = c.proxyUrl.Hostname()
			res := ctx.Response()
			c.proxy.ServeHTTP(res, req)
			return nil
		} else if err != nil {
			return err
		}

		h := ctx.Response().Header()
		h.Set("X-Ouchi-Cache", "cached")
		c.setConfiguredHeaders(h)

		return ctx.Blob(
			http.StatusOK,
			d.ContentType,
			d.Data,
		)
	}
}

func (c *TtlCache) Middleware() echo.MiddlewareFunc {
	return c.middlewareHandler
}
