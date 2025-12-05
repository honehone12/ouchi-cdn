package memory

import (
	"errors"
	"ouchi/cache"
	"ouchi/log"
	"slices"
	"sync"
	"time"
)

type MemoryStore struct {
	logger log.Logger

	cacheMap sync.Map
	ttlSec   time.Duration
	tickSec  time.Duration
}

const EOL_DATA_KEY = "EOL_DATA_KEY"

func NewMemoryStore(
	logger log.Logger,
	tickSec time.Duration,
	ttlSec time.Duration,
) *MemoryStore {
	m := &MemoryStore{
		logger: logger,

		cacheMap: sync.Map{},
		ttlSec:   ttlSec,
		tickSec:  tickSec,
	}

	// store sorted slice at key of 0
	m.cacheMap.Store(EOL_DATA_KEY, make([]cache.EolData, 0))

	go m.cleaning()

	return m
}

func (m *MemoryStore) cleaning() {
	ticker := time.Tick(m.tickSec)

	for t := range ticker {
		m.logger.Debugf("cleaning... now: %s", t)
		now := t.Unix()

		s, ok := m.cacheMap.Load(EOL_DATA_KEY)
		if !ok {
			m.logger.Error("failed to load sorted eol list")
			continue
		}
		sorted, ok := s.([]cache.EolData)
		if !ok {
			m.logger.Error("failed to cast sorted eol list")
			continue
		}

		for _, eolData := range sorted {
			if eolData.Eol >= now {
				break
			}

			m.cacheMap.Delete(eolData.Key)
			m.logger.Debugf("deleted: %s", eolData.Key)
		}

		sorted = slices.DeleteFunc(sorted, func(eolData cache.EolData) bool {
			return eolData.Eol < now
		})
		m.cacheMap.Store(EOL_DATA_KEY, sorted)
	}
}

func (m *MemoryStore) Get(url string) (*cache.ChacheData, error) {
	m.logger.Debugf("looking for %s", url)

	hash, err := cache.HashKey(url)
	if err != nil {
		return nil, err
	}

	v, ok := m.cacheMap.Load(hash)
	if !ok {
		return nil, cache.ErrNoSuchKey
	}
	d, ok := v.(*cache.ChacheData)
	if !ok {
		return nil, errors.New("failed to acquire value as expexted structure type")
	}

	now := time.Now().Unix()
	if d.Eol < now {
		return nil, cache.ErrExpired
	}

	m.logger.Debugf("found cache: %s", url)
	return d, nil
}

func (m *MemoryStore) Set(
	url string,
	contentType string,
	contentEncoding string,
	content []byte,
) error {
	eol := time.Now().Add(m.ttlSec).Unix()
	d := &cache.ChacheData{
		Eol:             eol,
		ContentType:     contentType,
		ContentEncoding: contentEncoding,
		Data:            content,
	}

	hash, err := cache.HashKey(url)
	if err != nil {
		return err
	}

	s, ok := m.cacheMap.Load(EOL_DATA_KEY)
	if !ok {
		return errors.New("could not find key for sorted eol list")
	}
	sorted, ok := s.([]cache.EolData)
	if !ok {
		return errors.New("failed to cast sorted eol list")
	}

	sorted = append(sorted, cache.EolData{
		Key: hash,
		Eol: eol,
	})
	slices.SortFunc(sorted, cache.SortEolData)
	m.cacheMap.Store(EOL_DATA_KEY, sorted)

	m.cacheMap.Store(hash, d)
	m.logger.Debugf(
		"cached: [url] %s, [type] %s, [enc] %s, [hash] %s",
		url,
		contentType,
		contentEncoding,
		hash,
	)
	return nil
}
