package ttlcache

import (
	"errors"
	"ouchi/logger"
	"sync"
	"time"

	"lukechampine.com/blake3"
)

type ChacheData struct {
	eol  int64
	data []byte
}

type TtlCache struct {
	ttl      time.Duration
	tick     time.Duration
	cacheMap sync.Map
	eolMap   sync.Map
	logger   logger.Logger
}

var ErrNoSuchKey error = errors.New("no such key")
var ErrExpired error = errors.New("ttl expired")

func NewTtlCache(ttl time.Duration, tick time.Duration, logger logger.Logger) *TtlCache {
	c := &TtlCache{
		ttl:      ttl,
		tick:     tick,
		cacheMap: sync.Map{},
		eolMap:   sync.Map{},
		logger:   logger,
	}
	c.startCleaning()
	return c
}

func (c *TtlCache) startCleaning() {
	go c.cleaning()
}

func (c *TtlCache) clean(key, value any, now int64) bool {
	eol, ok := key.(int64)
	if !ok || eol < now {
		c.logger.Debugf("deleting key: %d, value: %s", key, value)
		c.eolMap.Delete(key)
		c.cacheMap.Delete(value)
		return true
	}

	return false
}

func (c *TtlCache) cleaning() {
	ticker := time.Tick(c.tick)

	for now := range ticker {
		nowUnix := now.Unix()

		c.eolMap.Range(func(k, v any) bool {
			return c.clean(k, v, nowUnix)
		})
	}
}

func (c *TtlCache) Get(url string) (*ChacheData, error) {
	k := blake3.Sum256([]byte(url))
	v, ok := c.cacheMap.Load(k)
	if !ok {
		return nil, ErrNoSuchKey
	}
	d, ok := v.(*ChacheData)
	if !ok {
		return nil, errors.New("failed to acquire value as expexted structure type")
	}

	now := time.Now().Unix()
	if d.eol < now {
		return nil, ErrExpired
	}

	return d, nil
}

func (c *TtlCache) Set(url string, content []byte) {
	k := blake3.Sum256([]byte(url))
	eol := time.Now().Add(c.ttl).Unix()
	d := &ChacheData{
		eol:  eol,
		data: content,
	}

	c.cacheMap.Store(k, d)
	c.eolMap.Store(eol, k)
}
