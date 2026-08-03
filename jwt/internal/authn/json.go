package authn

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

var (
	ErrMalformedJSON = errors.New("authn: malformed JSON")
	ErrDuplicateJSON = errors.New("authn: duplicate JSON member")
)

type JSONOptions struct {
	MaxBytes   int
	MaxDepth   int
	MaxMembers int
}

// ValidateJSON validates exactly one JSON value and rejects duplicate object
// members at every nesting level.
func ValidateJSON(data []byte, options JSONOptions) error {
	if options.MaxBytes <= 0 || options.MaxDepth <= 0 || options.MaxMembers <= 0 {
		return ErrInvalidSize
	}
	if len(data) > options.MaxBytes {
		return ErrLimitExceeded
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	members := 0
	if err := parseJSONValue(decoder, 1, options, &members); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("%w: trailing value", ErrMalformedJSON)
		}
		return fmt.Errorf("%w: %v", ErrMalformedJSON, err)
	}
	return nil
}

func parseJSONValue(decoder *json.Decoder, depth int, options JSONOptions, members *int) error {
	if depth > options.MaxDepth {
		return ErrLimitExceeded
	}
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("%w: %v", ErrMalformedJSON, err)
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return fmt.Errorf("%w: %v", ErrMalformedJSON, err)
			}
			key, ok := keyToken.(string)
			if !ok {
				return ErrMalformedJSON
			}
			(*members)++
			if *members > options.MaxMembers {
				return ErrLimitExceeded
			}
			if _, duplicate := seen[key]; duplicate {
				return ErrDuplicateJSON
			}
			seen[key] = struct{}{}
			if err := parseJSONValue(decoder, depth+1, options, members); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			(*members)++
			if *members > options.MaxMembers {
				return ErrLimitExceeded
			}
			if err := parseJSONValue(decoder, depth+1, options, members); err != nil {
				return err
			}
		}
	default:
		return ErrMalformedJSON
	}
	closing, err := decoder.Token()
	if err != nil || closing != matchingDelimiter(delimiter) {
		return ErrMalformedJSON
	}
	return nil
}

func matchingDelimiter(open json.Delim) json.Delim {
	if open == '{' {
		return '}'
	}
	return ']'
}
