package ttlcache

import (
	"errors"
	"time"

	"github.com/labstack/echo/v4"
)

type TtlCache interface {
	Middleware() echo.MiddlewareFunc
}

type TtlCacheConfig struct {
	ProxyUrl string
	Ttl      time.Duration
	Tick     time.Duration
	Headers  map[string]string
	Logger   Logger
}

type ChacheData struct {
	Eol             int64
	ContentType     string
	ContentEncoding string
	Data            []byte
}

var ErrNoSuchKey error = errors.New("no such key")
var ErrExpired error = errors.New("ttl expired")
