// Package geo resolves an IP address to an approximate location using the
// free ip-api.com service (no API key, http only on the free tier).
package geo

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

// Location is the resolved place for an IP. Resolved is false only when the
// lookup itself fails (network/timeout/non-success response).
type Location struct {
	City        string
	Region      string
	Country     string
	CountryCode string
	Lat         float64
	Lon         float64
	Resolved    bool
}

var client = &http.Client{Timeout: 2 * time.Second}

// Lookup resolves ip to a Location. For loopback/private addresses (i.e. local
// development) it resolves the caller's *own* public IP instead, so you still
// see a real city while testing. Real internet visitors arrive with public IPs
// and resolve normally. It never errors — on failure it returns an unresolved
// Location so the caller can still log the raw IP.
func Lookup(ctx context.Context, ip string) Location {
	parsed := net.ParseIP(ip)
	query := ip
	if parsed == nil || parsed.IsLoopback() || parsed.IsPrivate() || parsed.IsUnspecified() {
		query = "" // empty path -> ip-api resolves this machine's public IP
	}

	url := fmt.Sprintf("http://ip-api.com/json/%s?fields=status,country,countryCode,regionName,city,lat,lon", query)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Location{City: "UNKNOWN", Resolved: false}
	}
	resp, err := client.Do(req)
	if err != nil {
		return Location{City: "UNKNOWN", Resolved: false}
	}
	defer resp.Body.Close()

	var body struct {
		Status      string  `json:"status"`
		Country     string  `json:"country"`
		CountryCode string  `json:"countryCode"`
		RegionName  string  `json:"regionName"`
		City        string  `json:"city"`
		Lat         float64 `json:"lat"`
		Lon         float64 `json:"lon"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Status != "success" {
		return Location{City: "UNKNOWN", Resolved: false}
	}
	return Location{
		City:        body.City,
		Region:      body.RegionName,
		Country:     body.Country,
		CountryCode: body.CountryCode,
		Lat:         body.Lat,
		Lon:         body.Lon,
		Resolved:    true,
	}
}
