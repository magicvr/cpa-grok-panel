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

type AuthDocument map[string]any

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

func (host *Host) GetAuthFile(authIndex string) (AuthDocument, error) {
	if host == nil || host.call == nil {
		return nil, errors.New("host callback is unavailable")
	}
	payload, err := json.Marshal(map[string]string{"auth_index": authIndex})
	if err != nil {
		return nil, fmt.Errorf("encode host.auth.get request: %w", err)
	}
	response, err := host.call("host.auth.get", payload)
	if err != nil {
		return nil, err
	}
	var result struct {
		AuthIndex string          `json:"auth_index"`
		Name      string          `json:"name"`
		JSON      json.RawMessage `json:"json"`
	}
	if err := DecodeEnvelope(response, &result); err != nil {
		if directErr := json.Unmarshal(response, &result); directErr != nil || len(result.JSON) == 0 {
			return nil, fmt.Errorf("decode host.auth.get response: %w", err)
		}
	}
	if len(result.JSON) == 0 || string(result.JSON) == "null" {
		return nil, errors.New("decode host.auth.get response: missing json field")
	}
	document, err := decodeAuthDocument(result.JSON)
	if err != nil {
		return nil, fmt.Errorf("decode host.auth.get response: %w", err)
	}
	return document, nil
}

func (host *Host) SaveAuthFile(name string, document AuthDocument) error {
	if host == nil || host.call == nil {
		return errors.New("host callback is unavailable")
	}
	raw, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode auth document: %w", err)
	}
	payload, err := json.Marshal(struct {
		Name string          `json:"name"`
		JSON json.RawMessage `json:"json"`
	}{Name: name, JSON: raw})
	if err != nil {
		return fmt.Errorf("encode host.auth.save request: %w", err)
	}
	response, err := host.call("host.auth.save", payload)
	if err != nil {
		return err
	}
	if err := DecodeEnvelope(response, nil); err != nil {
		return fmt.Errorf("host.auth.save: %w", err)
	}
	return nil
}

func decodeAuthDocument(raw json.RawMessage) (AuthDocument, error) {
	var encoded string
	if len(raw) > 0 && raw[0] == '"' && json.Unmarshal(raw, &encoded) == nil {
		raw = json.RawMessage(encoded)
	}
	var document AuthDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	return document, nil
}
