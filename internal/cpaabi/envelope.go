package cpaabi

import (
	"encoding/json"
	"fmt"
)

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable,omitempty"`
}

type Envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

func Success(result any) []byte {
	data, err := json.Marshal(struct {
		OK     bool `json:"ok"`
		Result any  `json:"result"`
	}{OK: true, Result: result})
	if err != nil {
		return Failure("internal_error", err.Error(), false)
	}
	return data
}

func Failure(code, message string, retryable bool) []byte {
	data, _ := json.Marshal(struct {
		OK    bool   `json:"ok"`
		Error *Error `json:"error"`
	}{OK: false, Error: &Error{Code: code, Message: message, Retryable: retryable}})
	return data
}

func DecodeEnvelope(data []byte, target any) error {
	var envelope Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return fmt.Errorf("decode host envelope: %w", err)
	}
	if !envelope.OK {
		if envelope.Error == nil {
			return fmt.Errorf("host call failed")
		}
		return fmt.Errorf("host call failed: %s: %s", envelope.Error.Code, envelope.Error.Message)
	}
	if target == nil || len(envelope.Result) == 0 || string(envelope.Result) == "null" {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, target); err != nil {
		return fmt.Errorf("decode host result: %w", err)
	}
	return nil
}
