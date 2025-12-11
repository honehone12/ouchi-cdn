package cache

import "errors"

type ChacheStore interface {
	Get(url string) (*ChacheData, error)
	Set(
		url string,
		/* or receive map[string]string */
		contentType string,
		content []byte,
	) error
}

type ChacheData struct {
	Eol         int64
	ContentType string
	Data        []byte
}

type EolData struct {
	Key string
	Eol int64
}

func SortEolData(a, b EolData) int {
	if a == b {
		return 0
	} else if a.Eol > b.Eol {
		return 1
	} else {
		return -1
	}
}

var ErrNoSuchKey error = errors.New("no such key")
var ErrExpired error = errors.New("ttl expired")
