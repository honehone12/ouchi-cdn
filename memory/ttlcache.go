package memory

import (
	"bytes"
	"errors"
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
	ttlSec   time.Duration
	tickSec  time.Duration
	headers  map[string]string
	cacheMap sync.Map

	logger   ttlcache.Logger
	proxyUrl *url.URL
	proxy    *httputil.ReverseProxy
}

const EOL_DATA_KEY = "EOL_DATA_KEY"

func NewMemoryTtlCache(config ttlcache.TtlCacheConfig) (*MemoryTtlCache, error) {
	proxyUrl, err := url.Parse(config.ProxyUrl)
	if err != nil {
		return nil, err
	}
	proxy := httputil.NewSingleHostReverseProxy(proxyUrl)

	c := &MemoryTtlCache{
		ttlSec:   config.TtlSec,
		tickSec:  config.TickSec,
		headers:  config.Headers,
		cacheMap: sync.Map{},

		logger:   config.Logger,
		proxyUrl: proxyUrl,
		proxy:    proxy,
	}

	// Use modifier for reading and caching response
	c.proxy.ModifyResponse = c.onProxyResponse
	// Store sorted slice at key of 0
	c.cacheMap.Store(EOL_DATA_KEY, make([]ttlcache.EolData, 0))
	c.startCleaning()
	return c, nil
}

func isWebSocketRequest(req *http.Request) bool {
	return strings.ToLower(req.Header.Get("Upgrade")) == "websocket"
}

func (c *MemoryTtlCache) hashKey(key string) (string, error) {
	hasher := fnv.New128a()
	if _, err := hasher.Write([]byte(key)); err != nil {
		return "", err
	}
	hash := string(hasher.Sum(nil))
	return hash, nil
}

func (c *MemoryTtlCache) setHeaders(
	ctx echo.Context,
	/* or receive map[string]string */
	contentEncoding string,
	cached bool,
) {
	headers := ctx.Response().Header()

	for k, v := range c.headers {
		headers.Set(k, v)
	}

	if len(contentEncoding) != 0 {
		headers.Set("Content-Encoding", contentEncoding)
	}

	if cached {
		headers.Set("XOuchiCache", "cachd")
	} else {
		headers.Set("XOuchiCache", "miss")
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
			c.setHeaders(ctx, "", false)
			return nil
		}

		cache, err := c.get(req.URL.RequestURI())
		// Cache miss - proxy the request
		if errors.Is(err, ttlcache.ErrNoSuchKey) || errors.Is(err, ttlcache.ErrExpired) {
			c.logger.Debug(err)
			req.Host = c.proxyUrl.Hostname()
			c.proxy.ServeHTTP(ctx.Response(), req)
			c.setHeaders(ctx, "", false)
			return nil
		} else if err != nil {
			return err
		}

		c.setHeaders(ctx, cache.ContentEncoding, true)

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
	ticker := time.Tick(c.tickSec)

	for t := range ticker {
		c.logger.Debugf("cleaning... now: %d", t)
		now := t.Unix()

		s, ok := c.cacheMap.Load(EOL_DATA_KEY)
		if !ok {
			c.logger.Error("failed to load sorted eol list")
			continue
		}
		sorted, ok := s.([]ttlcache.EolData)
		if !ok {
			c.logger.Error("failed to cast sorted eol list")
			continue
		}

		for _, eolData := range sorted {
			if eolData.Eol >= now {
				break
			}

			c.cacheMap.Delete(eolData.Key)
			c.logger.Debugf("deleted: %s", eolData.Key)
		}

		sorted = slices.DeleteFunc(sorted, func(eolData ttlcache.EolData) bool {
			return eolData.Eol < now
		})
		c.cacheMap.Store(EOL_DATA_KEY, sorted)
	}
}

func (c *MemoryTtlCache) get(url string) (*ttlcache.ChacheData, error) {
	c.logger.Debugf("looking for %s", url)

	hash, err := c.hashKey(url)
	if err != nil {
		return nil, err
	}

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
	eol := time.Now().Add(c.ttlSec).Unix()
	d := &ttlcache.ChacheData{
		Eol:             eol,
		ContentType:     contentType,
		ContentEncoding: contentEncoding,
		Data:            content,
	}

	hash, err := c.hashKey(url)
	if err != nil {
		return err
	}

	s, ok := c.cacheMap.Load(EOL_DATA_KEY)
	if !ok {
		return errors.New("could not find key for sorted eol list")
	}
	sorted, ok := s.([]ttlcache.EolData)
	if !ok {
		return errors.New("failed to cast sorted eol list")
	}

	sorted = append(sorted, ttlcache.EolData{
		Key: hash,
		Eol: eol,
	})
	slices.SortFunc(sorted, ttlcache.SortEolData)
	c.cacheMap.Store(EOL_DATA_KEY, sorted)

	c.cacheMap.Store(hash, d)
	c.logger.Debugf(
		"cached: [url] %s, [type] %s, [enc] %s, [hash] %s",
		url,
		contentType,
		contentEncoding,
		hash,
	)
	return nil
}
