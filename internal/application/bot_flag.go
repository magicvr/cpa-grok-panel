package application

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"

	"github.com/magicvr/cpa-grok-panel/internal/cpaabi"
)

type botFlagResult struct {
	flagged bool
	known   bool
	source  any
}

var accessTokenPaths = [][]string{
	{"access_token"},
	{"credentials", "access_token"},
	{"auth", "access_token"},
	{"oauth", "access_token"},
	{"tokens", "access_token"},
}

func detectBotFlag(document cpaabi.AuthDocument) botFlagResult {
	token := extractAccessToken(document)
	if token == "" {
		return botFlagResult{}
	}
	payload, ok := decodeJWTPayload(token)
	if !ok {
		return botFlagResult{}
	}
	result := botFlagResult{known: true}
	for _, value := range botFlagSources(payload) {
		source := scalarBotFlagSource(value)
		if result.source == nil && source != nil {
			result.source = source
		}
		if isBotFlagValue(value) {
			result.flagged = true
			result.source = source
			return result
		}
	}
	return result
}

func extractAccessToken(document cpaabi.AuthDocument) string {
	if token := accessTokenFromObject(document); token != "" {
		return token
	}
	nested, ok := nestedJSONObject(document["json"])
	if !ok {
		return ""
	}
	return accessTokenFromObject(nested)
}

func accessTokenFromObject(object any) string {
	for _, path := range accessTokenPaths {
		value, ok := lookupPath(object, path...)
		if !ok {
			continue
		}
		if token, ok := value.(string); ok && strings.TrimSpace(token) != "" {
			return strings.TrimSpace(token)
		}
	}
	return ""
}

func lookupPath(object any, path ...string) (any, bool) {
	current := object
	for _, key := range path {
		var next any
		var ok bool
		switch typed := current.(type) {
		case cpaabi.AuthDocument:
			next, ok = typed[key]
		case map[string]any:
			next, ok = typed[key]
		default:
			return nil, false
		}
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func nestedJSONObject(value any) (any, bool) {
	switch typed := value.(type) {
	case cpaabi.AuthDocument:
		return typed, true
	case map[string]any:
		return typed, true
	case string:
		var object map[string]any
		if json.Unmarshal([]byte(typed), &object) == nil {
			return object, true
		}
	}
	return nil, false
}

func decodeJWTPayload(token string) (map[string]any, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[1] == "" {
		return nil, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		raw, err = base64.URLEncoding.DecodeString(parts[1])
		if err != nil {
			return nil, false
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var payload map[string]any
	if err := decoder.Decode(&payload); err != nil || payload == nil {
		return nil, false
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, false
	}
	return payload, true
}

func botFlagSources(payload map[string]any) []any {
	values := make([]any, 0, 3)
	if value, ok := payload["bot_flag_source"]; ok {
		values = append(values, value)
	}
	for _, key := range []string{"bot", "user"} {
		if value, ok := lookupPath(payload, key, "bot_flag_source"); ok {
			values = append(values, value)
		}
	}
	return values
}

func isBotFlagValue(value any) bool {
	switch typed := value.(type) {
	case string:
		return typed == "1"
	case json.Number:
		number, err := typed.Float64()
		return err == nil && number == 1
	case int:
		return typed == 1
	case int8:
		return typed == 1
	case int16:
		return typed == 1
	case int32:
		return typed == 1
	case int64:
		return typed == 1
	case uint:
		return typed == 1
	case uint8:
		return typed == 1
	case uint16:
		return typed == 1
	case uint32:
		return typed == 1
	case uint64:
		return typed == 1
	case float32:
		return typed == 1
	case float64:
		return typed == 1
	default:
		return false
	}
}

func scalarBotFlagSource(value any) any {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return typed
	default:
		return nil
	}
}
