package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"

	"github.com/carvebloom/authova/internal/application"
	"github.com/carvebloom/authova/internal/domain"
)

func privateInput(value any) error {
	return decodePrivateInput(os.Stdin, value)
}

func decodePrivateInput(input io.Reader, value any) error {
	const maximum = 1024 * 1024
	data, err := io.ReadAll(io.LimitReader(input, maximum+1))
	if err != nil || len(data) > maximum {
		return errors.New("private maintenance input exceeds limit or is unreadable")
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("maintenance input must be a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errors.New("invalid private maintenance input")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("maintenance input must contain exactly one JSON object")
	}
	return nil
}

func recoverIdentities(ctx context.Context, s *application.Service, command string) error {
	switch command {
	case "quarantine-identities":
		var in struct {
			Reason  string `json:"reason"`
			Confirm string `json:"confirm"`
		}
		if err := privateInput(&in); err != nil {
			return err
		}
		if in.Confirm != "quarantine_restored_identities" {
			return errors.New("requires confirm=quarantine_restored_identities; isolate ingress and stop all writers")
		}
		report, err := s.QuarantineRecoveredIdentities(ctx, in.Reason)
		if err != nil {
			slog.Error("Identity quarantine incomplete; keep ingress isolated", "committed", report)
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(report)
	case "recover-user":
		var in struct {
			AppID    string `json:"app_id"`
			UserID   string `json:"user_id"`
			Kind     string `json:"kind"`
			Value    string `json:"value"`
			Password string `json:"password"`
			Role     string `json:"admin_role"`
			Reason   string `json:"reason"`
			Confirm  string `json:"confirm"`
		}
		if err := privateInput(&in); err != nil {
			return err
		}
		if in.Confirm != "identity_and_contact_verified" {
			return errors.New("requires confirm=identity_and_contact_verified after out-of-band verification")
		}
		scope, err := domain.AppScope(in.AppID)
		if err != nil {
			return err
		}
		return s.RecoverQuarantinedUser(ctx, scope, in.UserID, in.Kind, in.Value, in.Password, in.Role, in.Reason)
	case "release-recovered-app":
		var in struct {
			AppID   string `json:"app_id"`
			Reason  string `json:"reason"`
			Confirm string `json:"confirm"`
		}
		if err := privateInput(&in); err != nil {
			return err
		}
		if in.Confirm != "external_facts_reconciled" {
			return errors.New("requires confirm=external_facts_reconciled; remaining users stay quarantined")
		}
		scope, err := domain.AppScope(in.AppID)
		if err != nil {
			return err
		}
		return s.ReleaseRecoveredApp(ctx, scope, in.Reason)
	}
	return domain.ErrInvalid
}
