package main

import (
	"encoding/json"
	"errors"
	"strings"
)

// The setup of korb as tools, for an assistant's admin page (not for a
// model): logging in without a terminal and choosing the delivery service
// or a market. The login is the owner's: an assistant offers these tools to
// an admin, never to a model.

const loginHint = `Open authUrl in Chrome and sign in to REWE. At the end Chrome cannot open the address de.rewe.app://redirect?…: ` +
	`open the developer tools (F12), tab Console, and copy that address from the line "Failed to launch 'de.rewe.app://redirect?…'". ` +
	`Pass it to login_complete within 30 minutes.`

func (ap *app) mcpLoginStart(json.RawMessage) (any, error) {
	authURL, _, err := ap.tokens.StartLogin()
	if err != nil {
		return nil, err
	}
	return map[string]any{"authUrl": authURL, "hint": loginHint}, nil
}

func (ap *app) mcpLoginComplete(raw json.RawMessage) (any, error) {
	var a struct{ URL string }
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.URL) == "" {
		return nil, errors.New("url is required: the de.rewe.app://redirect?… address from the browser")
	}
	if err := ap.tokens.CompleteLogin(ap.ctx, a.URL); err != nil {
		return nil, err
	}
	return map[string]any{"loggedIn": true}, nil
}

func (ap *app) mcpStoreSearch(raw json.RawMessage) (any, error) {
	var a struct{ Zip string }
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if strings.TrimSpace(a.Zip) == "" {
		return nil, errors.New("zip is required")
	}
	markets, _, err := ap.marketsFor(strings.TrimSpace(a.Zip))
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, m := range markets {
		v := map[string]any{"id": m.WwIdent, "name": m.DisplayName, "service": strings.ToLower(m.ServiceType)}
		if m.ServiceType != "DELIVERY" {
			v["address"] = strings.Trim(m.StreetWithHouseNumber+", "+strings.TrimSpace(m.ZipCode+" "+m.City), ", ")
		}
		out = append(out, v)
	}
	return out, nil
}

func (ap *app) mcpStoreSet(raw json.RawMessage) (any, error) {
	var a struct{ ID, Zip string }
	if err := decodeArgs(raw, &a); err != nil {
		return nil, err
	}
	if a.ID == "" || a.Zip == "" {
		return nil, errors.New("id and zip are required")
	}
	s, err := ap.chooseStore(strings.TrimSpace(a.ID), strings.TrimSpace(a.Zip))
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": s.WwIdent, "zip": s.ZipCode, "service": strings.ToLower(s.ServiceType)}, nil
}
