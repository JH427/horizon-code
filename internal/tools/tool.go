package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/JH427/horizon-code/internal/model"
)

type Result struct {
	Content  string
	Changed  bool
	Verified bool
}

type Tool interface {
	Definition() model.Tool
	Run(context.Context, []byte) (Result, error)
}

func decodeStrictArguments[T any](raw []byte, destination *T) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("trailing JSON value after tool arguments")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode trailing tool arguments: %w", err)
	}
	return nil
}
