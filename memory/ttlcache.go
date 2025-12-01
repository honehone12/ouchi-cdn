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
	"slices"
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
	c.proxy.ModifyResponse = c.onProxyResponse
	// Store sorted slice at key of 0
	c.eolMap.Store(0, make([]int64, 0))
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

func (c *MemoryTtlCache) onProxyResponse(res *http.Response) error {
	if res.StatusCode == http.StatusOK {
		h := res.Header
		cacheControl := h.Get("Cache-Control")
		if cacheControl != "no-cache" && cacheControl != "no-store" {
			body, err := io.ReadAll(res.Body)
			if err != nil {
				return err
			}
			// Close now to set new body
			res.Body.Close()

			go c.cacheResponse(
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

func (c *MemoryTtlCache) cacheResponse(
	url string,
	contentType string,
	contentEncoding string,
	body []byte,
) {
	// Cache fail should never affect to response

	if err := c.set(url, contentType, contentEncoding, body); err != nil {
		c.logger.Error("failed to set cache", err)
		return
	}
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

func (c *MemoryTtlCache) cleaning() {
	ticker := time.Tick(c.tick)

	for now := range ticker {
		c.logger.Debug("cleaning")
		nowUnix := now.Unix()

		s, ok := c.eolMap.Load(0)
		if !ok {
			c.logger.Error("failed to load sorted eol list")
			continue
		}
		sorted, ok := s.([]int64)
		if !ok {
			c.logger.Error("failed to cast sorted eol list")
			continue
		}

		for _, eol := range sorted {
			if eol >= nowUnix {
				break
			}

			v, ok := c.eolMap.Load(eol)
			if !ok {
				c.logger.Errorf("failed to load [%d]", eol)
				continue
			}

			c.eolMap.Delete(eol)
			c.cacheMap.Delete(v)
			c.logger.Debugf("deleted: %s", v)
		}

		sorted = slices.DeleteFunc(sorted, func(eol int64) bool {
			return eol < nowUnix
		})
		c.eolMap.Store(0, sorted)
	}
}

func (c *MemoryTtlCache) get(url string) (*ttlcache.ChacheData, error) {
	c.logger.Debugf("looking for %s", url)
	hash := string(c.hasher.Sum([]byte(url)))

	v, ok := c.cacheMap.Load(hash)
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
) error {
	eol := time.Now().Add(c.ttl).Unix()
	d := &ttlcache.ChacheData{
		Eol:             eol,
		ContentType:     contentType,
		ContentEncoding: contentEncoding,
		Data:            content,
	}
	hash := string(c.hasher.Sum([]byte(url)))

	s, ok := c.eolMap.Load(0)
	if !ok {
		return errors.New("could not find key for sorted eol list")
	}
	sorted, ok := s.([]int64)
	if !ok {
		return errors.New("failed to cast sorted eol list")
	}

	sorted = append(sorted, eol)
	slices.Sort(sorted)
	c.eolMap.Store(0, sorted)

	c.cacheMap.Store(hash, d)
	c.eolMap.Store(eol, hash)
	c.logger.Debugf("cached: %s, %s, %s", url, contentType, contentEncoding)
	return nil
}
