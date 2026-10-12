package desktop

import "errors"

type StaticSecretProvider []byte

func (p StaticSecretProvider) MasterKey() ([]byte, error) {
	if len(p) != 32 {
		return nil, errors.New("desktop master key must be 32 bytes")
	}
	return append([]byte(nil), p...), nil
}
