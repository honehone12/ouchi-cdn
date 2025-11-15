package ttlcache

import (
	"encoding/json"
	"os"
	"time"
)

type ConfigFile struct {
	Headers    map[string]string `json:"headers"`
	TtlSec     time.Duration     `json:"ttl_sec"`
	TickSec    time.Duration     `json:"tick_sec"`
	OriginPort uint16            `json:"origin_port"`
	ListenPort uint16            `json:"listen_port"`
}

func ReadConfigFile(configFile string) (*ConfigFile, error) {
	b, err := os.ReadFile(configFile)
	if err != nil {
		return nil, err
	}

	c := &ConfigFile{}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, err
	}

	return c, nil
}
