package memory

import (
	"bytes"
	"errors"
	"hash"
	"hash/fnv"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"ouchi/ttlcache"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
)

type MemoryTtlCache struct {
	ttl      time.Duration
	tick     time.Duration
	headers  map[string]string
	cacheMap sync.Map
	eolMap   sync.Map

	hasher   hash.Hash
	logger   ttlcache.Logger
	proxyUrl *url.URL
	proxy    *httputil.ReverseProxy
}

func NewMemoryTtlCache(config ttlcache.TtlCacheConfig) (*MemoryTtlCache, error) {
	proxyUrl, err := url.Parse(config.ProxyUrl)
	if err != nil {
		return nil, err
	}
	proxy := httputil.NewSingleHostReverseProxy(proxyUrl)

	c := &MemoryTtlCache{
		ttl:      config.Ttl,
		tick:     config.Tick,
		headers:  config.Headers,
		cacheMap: sync.Map{},
		eolMap:   sync.Map{},

		hasher:   fnv.New128a(),
		logger:   config.Logger,
		proxyUrl: proxyUrl,
		proxy:    proxy,
	}

	// Use modifier for reading and caching response
	c.proxy.ModifyResponse = c.cacheResponse
	c.startCleaning()
	return c, nil
}

func isWebSocketRequest(req *http.Request) bool {
	return strings.ToLower(req.Header.Get("Upgrade")) == "websocket"
}

func (c *MemoryTtlCache) setHeaders(ctx echo.Context) {
	headers := ctx.Response().Header()
	for k, v := range c.headers {
		headers.Set(k, v)
	}
}

func (c *MemoryTtlCache) cacheResponse(res *http.Response) error {
	if res.StatusCode == http.StatusOK {
		h := res.Header
		cacheControl := h.Get("Cache-Control")
		if cacheControl != "no-cache" && cacheControl != "no-store" {
			defer res.Body.Close()
			body, err := io.ReadAll(res.Body)
			if err != nil {
				return err
			}

			c.set(
				res.Request.URL.RequestURI(),
				h.Get("Content-Type"),
				h.Get("Content-Encoding"),
				body,
			)

			// Set body again. better way ??
			res.Body = io.NopCloser(bytes.NewReader(body))
			res.Header.Set("XOuchCdn", "miss")
		}
	}

	return nil
}

func (c *MemoryTtlCache) middlewareHandler(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) error {
		req := ctx.Request()

		// WebSocket requests bypass cache and go directly to proxy
		if isWebSocketRequest(req) {
			c.logger.Infof("proxy websocket: %s", req.URL.String())
			req.Host = c.proxyUrl.Hostname()
			c.proxy.ServeHTTP(ctx.Response(), req)
			c.setHeaders(ctx)
			return nil
		}

		cache, err := c.get(req.URL.RequestURI())
		// Cache miss - proxy the request
		if errors.Is(err, ttlcache.ErrNoSuchKey) || errors.Is(err, ttlcache.ErrExpired) {
			c.logger.Debug(err)
			req.Host = c.proxyUrl.Hostname()
			c.proxy.ServeHTTP(ctx.Response(), req)
			c.setHeaders(ctx)
			return nil
		} else if err != nil {
			return err
		}

		c.logger.Debugf("cached headers: %s, %s", cache.ContentType, cache.ContentEncoding)
		h := ctx.Response().Header()
		h.Set("XOuchCdn", "cached")
		if len(cache.ContentEncoding) != 0 {
			h.Set("Content-Encoding", cache.ContentEncoding)
		}

		c.setHeaders(ctx)

		return ctx.Blob(
			http.StatusOK,
			cache.ContentType,
			cache.Data,
		)
	}
}

func (c *MemoryTtlCache) Middleware() echo.MiddlewareFunc {
	return c.middlewareHandler
}

func (c *MemoryTtlCache) startCleaning() {
	go c.cleaning()
}

func (c *MemoryTtlCache) clean(key, value any, now int64) bool {
	eol, ok := key.(int64)
	if !ok || eol < now {
		c.eolMap.Delete(key)
		c.cacheMap.Delete(value)
		c.logger.Debugf("deleted: %s", value)
	}

	return true
}

func (c *MemoryTtlCache) cleaning() {
	ticker := time.Tick(c.tick)

	for now := range ticker {
		c.logger.Debug("cleaning")
		nowUnix := now.Unix()

		c.eolMap.Range(func(k, v any) bool {
			return c.clean(k, v, nowUnix)
		})
	}
}

func (c *MemoryTtlCache) get(url string) (*ttlcache.ChacheData, error) {
	c.logger.Debugf("looking for %s", url)
	v, ok := c.cacheMap.Load(url)
	if !ok {
		return nil, ttlcache.ErrNoSuchKey
	}
	d, ok := v.(*ttlcache.ChacheData)
	if !ok {
		return nil, errors.New("failed to acquire value as expexted structure type")
	}

	now := time.Now().Unix()
	if d.Eol < now {
		return nil, ttlcache.ErrExpired
	}

	c.logger.Debugf("found cache: %s", url)
	return d, nil
}

func (c *MemoryTtlCache) set(
	url string,
	contentType string,
	contentEncoding string,
	content []byte,
) {
	eol := time.Now().Add(c.ttl).Unix()
	d := &ttlcache.ChacheData{
		Eol:             eol,
		ContentType:     contentType,
		ContentEncoding: contentEncoding,
		Data:            content,
	}

	c.cacheMap.Store(url, d)
	c.eolMap.Store(eol, url)
	c.logger.Debugf("cached: %s %s %s", url, contentType, contentEncoding)
}
