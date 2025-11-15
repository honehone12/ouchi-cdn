package headers

import (
	"encoding/json"
	"os"
)

func ReadHeadersConfig(config string) (map[string]string, error) {
	b, err := os.ReadFile(config)
	if err != nil {
		return nil, err
	}

	h := make(map[string]string)
	if err := json.Unmarshal(b, &h); err != nil {
		return nil, err
	}

	return h, nil
}
