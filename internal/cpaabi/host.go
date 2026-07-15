package cpaabi

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/magicvr/cpa-grok-panel/internal/domain"
)

type CallFunc func(method string, payload []byte) ([]byte, error)

type Host struct {
	call CallFunc
}

func NewHost(call CallFunc) *Host {
	return &Host{call: call}
}

func (host *Host) ListAuthFiles() ([]domain.AuthFile, error) {
	if host == nil || host.call == nil {
		return nil, errors.New("host callback is unavailable")
	}
	response, err := host.call("host.auth.list", []byte(`{}`))
	if err != nil {
		return nil, err
	}
	var result struct {
		Files []domain.AuthFile `json:"files"`
	}
	if err := DecodeEnvelope(response, &result); err == nil {
		return result.Files, nil
	}
	if err := json.Unmarshal(response, &result); err != nil {
		return nil, fmt.Errorf("decode host.auth.list response: %w", err)
	}
	return result.Files, nil
}
