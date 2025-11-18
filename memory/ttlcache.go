package memory

import (
	"errors"
	"hash"
	"hash/fnv"
	"io"
	"net/http"
	"ouchi/ttlcache"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
)

type MemoryTtlCache struct {
	origin   string
	ttl      time.Duration
	tick     time.Duration
	headers  map[string]string
	cacheMap sync.Map
	eolMap   sync.Map

	hasher    hash.Hash
	logger    ttlcache.Logger
	transport http.RoundTripper
}

func NewMemoryTtlCache(config ttlcache.TtlCacheConfig) *MemoryTtlCache {
	c := &MemoryTtlCache{
		origin:   config.Origin,
		ttl:      config.Ttl,
		tick:     config.Tick,
		headers:  config.Headers,
		cacheMap: sync.Map{},
		eolMap:   sync.Map{},

		hasher:    fnv.New128a(),
		logger:    config.Logger,
		transport: http.DefaultTransport,
	}
	c.startCleaning()
	return c
}

func (c *MemoryTtlCache) setHeaders(ctx echo.Context) {
	headers := ctx.Response().Header()
	for k, v := range c.headers {
		headers.Set(k, v)
	}
}

func (c *MemoryTtlCache) proxyRequest(ctx echo.Context) error {
	req := ctx.Request()
	k := req.URL.String()
	req.URL.Scheme = "http"
	req.URL.Host = c.origin

	pro, err := c.transport.RoundTrip(req)
	if err != nil {
		return err
	}
	defer pro.Body.Close()

	b, err := io.ReadAll(pro.Body)
	if err != nil {
		return err
	}

	if pro.StatusCode == http.StatusOK {
		cacheControl := pro.Header.Get("Cache-Control")
		if cacheControl != "no-cache" && cacheControl != "no-store" {
			c.set(k, pro.Header.Get("Content-Type"), b)
		}
	}

	res := ctx.Response()
	res.Status = pro.StatusCode
	h := res.Header()
	for k, vs := range pro.Header {
		for _, v := range vs {
			h.Set(k, v)
		}
	}

	if _, err := res.Write(b); err != nil {
		return err
	}

	return nil
}

func (c *MemoryTtlCache) middlewareHandler(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) error {
		cache, err := c.get(ctx.Request().URL.String())
		if errors.Is(err, ttlcache.ErrNoSuchKey) || errors.Is(err, ttlcache.ErrExpired) {
			if err := c.proxyRequest(ctx); err != nil {
				return err
			}

			ctx.Response().Header().Set("XOuchCdn", "miss")
		} else if err != nil {
			return err
		} else {
			if err := ctx.Blob(
				http.StatusOK,
				cache.ContentType,
				cache.Data,
			); err != nil {
				return err
			}

			ctx.Response().Header().Set("XOuchCdn", "cached")
		}

		c.setHeaders(ctx)

		return nil
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
	k := c.hasher.Sum([]byte(url))
	v, ok := c.cacheMap.Load(k)
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

	c.logger.Debugf("found cache: %s : %x", url, k)
	return d, nil
}

func (c *MemoryTtlCache) set(url string, contentType string, content []byte) {
	k := c.hasher.Sum([]byte(url))
	eol := time.Now().Add(c.ttl).Unix()
	d := &ttlcache.ChacheData{
		Eol:         eol,
		ContentType: contentType,
		Data:        content,
	}

	c.cacheMap.Store(k, d)
	c.eolMap.Store(eol, k)
	c.logger.Debugf("cached: %s : %x", url, k)
}
