package memory

import (
	"errors"
	"net/http"
	"ouchi/ttlcache"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"lukechampine.com/blake3"
)

type MemoryTtlCache struct {
	ttl      time.Duration
	tick     time.Duration
	headers  map[string]string
	cacheMap sync.Map
	eolMap   sync.Map
	logger   ttlcache.Logger
}

func NewMemoryTtlCache(config ttlcache.TtlCacheConfig) *MemoryTtlCache {
	c := &MemoryTtlCache{
		ttl:      config.Ttl,
		tick:     config.Tick,
		headers:  config.Headers,
		cacheMap: sync.Map{},
		eolMap:   sync.Map{},
		logger:   config.Logger,
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

func (c *MemoryTtlCache) middlewareHandler(next echo.HandlerFunc) echo.HandlerFunc {
	return func(ctx echo.Context) error {
		url := ctx.Request().URL.String()

		cache, err := c.get(url)
		if errors.Is(err, ttlcache.ErrNoSuchKey) || errors.Is(err, ttlcache.ErrExpired) {
			c.setHeaders(ctx)
			ctx.Response().Header().Set("XOuchCdn", "miss")
			return next(ctx)
		} else if err != nil {
			return err
		}

		c.logger.Debugf("using cached: %s", url)
		c.setHeaders(ctx)
		ctx.Response().Header().Set("XOuchCdn", "cached")
		if err := ctx.Blob(
			http.StatusOK,
			cache.ContentType,
			cache.Data,
		); err != nil {
			return err
		}

		return nil
	}
}

func (c *MemoryTtlCache) Middleware() echo.MiddlewareFunc {
	return c.middlewareHandler
}

func (c *MemoryTtlCache) bodyDumpHandler(ctx echo.Context, req, res []byte) {
	url := ctx.Request().URL.String()
	contentType := ctx.Response().Header().Get("Content-Type")

	cacheCtl := ctx.Response().Header().Get("Cache-Control")
	if cacheCtl == "no-cache" || cacheCtl == "no-store" {
		return
	}
	c.set(url, contentType, res)
	c.logger.Debugf("cached: %s", url)
}

func (c *MemoryTtlCache) BodyDump() middleware.BodyDumpHandler {
	return c.bodyDumpHandler
}

func (c *MemoryTtlCache) startCleaning() {
	go c.cleaning()
}

func (c *MemoryTtlCache) clean(key, value any, now int64) bool {
	eol, ok := key.(int64)
	if !ok || eol < now {
		c.logger.Debugf("deleting key: %d, value: %s", key, value)
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
	k := blake3.Sum256([]byte(url))
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

	return d, nil
}

func (c *MemoryTtlCache) set(url string, contentType string, content []byte) {
	k := blake3.Sum256([]byte(url))
	eol := time.Now().Add(c.ttl).Unix()
	d := &ttlcache.ChacheData{
		Eol:         eol,
		ContentType: contentType,
		Data:        content,
	}

	c.cacheMap.Store(k, d)
	c.eolMap.Store(eol, k)
}
