package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nyaruka/phonenumbers/v2"
)

// Configuration Struct
type Config struct {
	WHMCSURL         string
	WHMCSIdentifier  string
	WHMCSSecret      string
	ZoomAccountID    string
	ZoomClientID     string
	ZoomClientSecret string
}

// WHMCS Structs
type WHMCSClient struct {
	ID                   int    `json:"id"`
	FirstName            string `json:"firstname"`
	LastName             string `json:"lastname"`
	CompanyName          string `json:"companyname"`
	Email                string `json:"email"`
	PhoneNumber          string `json:"phonenumber"`
	FormattedPhoneNumber string `json:"phonenumberformatted"`
	Status               string `json:"status"`
}

type WHMCSResponse struct {
	Result  string `json:"result"`
	Clients struct {
		Client []WHMCSClient `json:"client"`
	} `json:"clients"`
}

// Zoom Structs
type ZoomTokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

type ExternalContact struct {
	ExternalContactID string   `json:"external_contact_id,omitempty"`
	ID                string   `json:"id"` // Maps to WHMCS Client ID
	Name              string   `json:"name"`
	Email             string   `json:"email,omitempty"`
	PhoneNumbers      []string `json:"phone_numbers"`
}

type ZoomListContactsResponse struct {
	NextPageToken    string            `json:"next_page_token"`
	ExternalContacts []ExternalContact `json:"external_contacts"`
}

func main() {
	if err := loadEnv(".env"); err != nil {
		log.Fatalf("Failed to load .env: %v", err)
	}

	cfg := Config{
		WHMCSURL:         os.Getenv("WHMCS_API_URL"),
		WHMCSIdentifier:  os.Getenv("WHMCS_IDENTIFIER"),
		WHMCSSecret:      os.Getenv("WHMCS_SECRET"),
		ZoomAccountID:    os.Getenv("ZOOM_ACCOUNT_ID"),
		ZoomClientID:     os.Getenv("ZOOM_CLIENT_ID"),
		ZoomClientSecret: os.Getenv("ZOOM_CLIENT_SECRET"),
	}
	for _, env := range []struct {
		name, value string
	}{
		{"WHMCS_API_URL", cfg.WHMCSURL},
		{"WHMCS_IDENTIFIER", cfg.WHMCSIdentifier},
		{"WHMCS_SECRET", cfg.WHMCSSecret},
		{"ZOOM_ACCOUNT_ID", cfg.ZoomAccountID},
		{"ZOOM_CLIENT_ID", cfg.ZoomClientID},
		{"ZOOM_CLIENT_SECRET", cfg.ZoomClientSecret},
	} {
		if env.value == "" {
			log.Fatalf("Missing required environment variable %s", env.name)
		}
	}

	httpClient := &http.Client{Timeout: 15 * time.Second}

	log.Println("Starting WHMCS -> Zoom Phone Nightly Sync...")

	// 1. Fetch Zoom OAuth Token
	zoomToken, err := getZoomAccessToken(httpClient, cfg)
	if err != nil {
		log.Fatalf("Failed to fetch Zoom Access Token: %v", err)
	}

	// 2. Fetch Active WHMCS Clients
	clients, err := fetchWHMCSClients(httpClient, cfg)
	if err != nil {
		log.Fatalf("Failed to fetch WHMCS Clients: %v", err)
	}
	log.Printf("Fetched %d active clients from WHMCS.\n", len(clients))
	withPhone, syncable := 0, 0
	for _, client := range clients {
		if client.PhoneNumber != "" {
			withPhone++
		}
		if formatE164(client.PhoneNumber) != "" {
			syncable++
		}
	}
	log.Printf("%d clients have phone numbers; %d are syncable.\n", withPhone, syncable)

	// 3. Fetch Existing Zoom External Contacts
	existingContacts, err := fetchExistingZoomContacts(httpClient, zoomToken)
	if err != nil {
		log.Fatalf("Failed to fetch Zoom external contacts: %v", err)
	}
	log.Printf("Fetched %d existing external contacts from Zoom.\n", len(existingContacts))

	// 4. Concurrent Sync Process using Worker Pool
	if err := syncClients(httpClient, zoomToken, clients, existingContacts); err != nil {
		log.Fatalf("Sync failed: %v", err)
	}

	log.Println("Nightly sync completed successfully!")
}

// --- WHMCS API FUNCTIONS ---

func fetchWHMCSClients(client *http.Client, cfg Config) ([]WHMCSClient, error) {
	data := url.Values{}
	data.Set("action", "GetClients")
	data.Set("identifier", cfg.WHMCSIdentifier)
	data.Set("secret", cfg.WHMCSSecret)
	data.Set("responsetype", "json")
	data.Set("limitnum", "5000")
	data.Set("status", "Active")

	resp, err := client.PostForm(cfg.WHMCSURL, data)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("WHMCS returned HTTP %d: %s", resp.StatusCode, body)
	}
	var whmcsResp WHMCSResponse
	if err := json.Unmarshal(body, &whmcsResp); err != nil {
		return nil, err
	}

	if whmcsResp.Result != "success" {
		return nil, fmt.Errorf("WHMCS API error: %s", string(body))
	}

	for i := range whmcsResp.Clients.Client {
		details, err := fetchWHMCSClientDetails(client, cfg, whmcsResp.Clients.Client[i].ID)
		if err != nil {
			return nil, err
		}
		if details.FormattedPhoneNumber != "" {
			details.PhoneNumber = details.FormattedPhoneNumber
		}
		whmcsResp.Clients.Client[i] = details
	}

	return whmcsResp.Clients.Client, nil
}

func fetchWHMCSClientDetails(client *http.Client, cfg Config, clientID int) (WHMCSClient, error) {
	data := url.Values{}
	data.Set("action", "GetClientsDetails")
	data.Set("identifier", cfg.WHMCSIdentifier)
	data.Set("secret", cfg.WHMCSSecret)
	data.Set("responsetype", "json")
	data.Set("clientid", strconv.Itoa(clientID))

	resp, err := client.PostForm(cfg.WHMCSURL, data)
	if err != nil {
		return WHMCSClient{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return WHMCSClient{}, fmt.Errorf("WHMCS client %d returned HTTP %d: %s", clientID, resp.StatusCode, body)
	}

	var result struct {
		Result  string      `json:"result"`
		Message string      `json:"message"`
		Client  WHMCSClient `json:"client"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return WHMCSClient{}, err
	}
	if result.Result != "success" {
		return WHMCSClient{}, fmt.Errorf("WHMCS client %d: %s", clientID, result.Message)
	}
	return result.Client, nil
}

// --- ZOOM API FUNCTIONS ---

func getZoomAccessToken(client *http.Client, cfg Config) (string, error) {
	tokenURL := "https://zoom.us/oauth/token?" + url.Values{
		"grant_type": {"account_credentials"},
		"account_id": {cfg.ZoomAccountID},
	}.Encode()

	req, err := http.NewRequest("POST", tokenURL, nil)
	if err != nil {
		return "", err
	}

	req.SetBasicAuth(cfg.ZoomClientID, cfg.ZoomClientSecret)

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("auth failed (%d): %s", resp.StatusCode, string(body))
	}

	var tokenResp ZoomTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", err
	}
	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("auth response did not include an access token")
	}

	return tokenResp.AccessToken, nil
}

func fetchExistingZoomContacts(client *http.Client, token string) (map[string]ExternalContact, error) {
	contacts := make(map[string]ExternalContact)
	nextPageToken := ""

	for {
		zoomURL := "https://api.zoom.us/v2/phone/external_contacts?page_size=300"
		if nextPageToken != "" {
			zoomURL += "&next_page_token=" + url.QueryEscape(nextPageToken)
		}

		req, err := http.NewRequest("GET", zoomURL, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("Zoom contact list failed (%d): %s", resp.StatusCode, body)
		}

		var listResp ZoomListContactsResponse
		err = json.NewDecoder(resp.Body).Decode(&listResp)
		resp.Body.Close()

		if err != nil {
			return nil, err
		}

		for _, contact := range listResp.ExternalContacts {
			if contact.ID != "" {
				contacts[contact.ID] = contact
			}
		}

		nextPageToken = listResp.NextPageToken
		if nextPageToken == "" {
			break
		}
	}

	return contacts, nil
}

// --- SYNC WORKER POOL ---

func syncClients(client *http.Client, token string, whmcsClients []WHMCSClient, existing map[string]ExternalContact) error {
	clients := clientsToSync(whmcsClients, existing)
	jobs := make(chan WHMCSClient, len(clients))
	results := make(chan error, len(clients))
	var wg sync.WaitGroup

	// Set concurrency limit to avoid hitting Zoom API rate limits (e.g., 5 parallel workers)
	workerCount := 5
	for i := 0; i < workerCount; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c := range jobs {
				results <- syncSingleClient(client, token, c, existing)
			}
		}()
	}

	for _, c := range clients {
		jobs <- c
	}
	close(jobs)

	wg.Wait()
	close(results)

	failures := 0
	for err := range results {
		if err != nil {
			failures++
			log.Printf("[FAIL] %v", err)
		}
	}
	if failures > 0 {
		return fmt.Errorf("%d of %d contacts failed", failures, len(clients))
	}
	return nil
}

func clientsToSync(clients []WHMCSClient, existing map[string]ExternalContact) []WHMCSClient {
	phoneOwners := make(map[string]string)
	for id, contact := range existing {
		for _, phone := range contact.PhoneNumbers {
			if phone = formatE164(phone); phone != "" {
				phoneOwners[phone] = id
			}
		}
	}

	selected := make([]WHMCSClient, 0, len(clients))
	for _, client := range clients {
		phone := formatE164(client.PhoneNumber)
		if phone == "" {
			continue
		}
		id := strconv.Itoa(client.ID)
		if owner, exists := phoneOwners[phone]; exists && owner != id {
			log.Printf("[SKIP] Client ID %s: phone number already belongs to client %s", id, owner)
			continue
		}
		phoneOwners[phone] = id
		selected = append(selected, client)
	}
	return selected
}

func syncSingleClient(client *http.Client, token string, whmcsClient WHMCSClient, existing map[string]ExternalContact) error {
	phone := formatE164(whmcsClient.PhoneNumber)
	clientIDStr := strconv.Itoa(whmcsClient.ID)
	displayName := fmt.Sprintf("%s %s", whmcsClient.FirstName, whmcsClient.LastName)
	displayName = strings.TrimSpace(displayName)
	if whmcsClient.CompanyName != "" {
		displayName = fmt.Sprintf("%s (%s)", displayName, whmcsClient.CompanyName)
	}

	payload := ExternalContact{
		ID:           clientIDStr,
		Name:         displayName,
		Email:        whmcsClient.Email,
		PhoneNumbers: []string{phone},
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("client %s: %w", clientIDStr, err)
	}

	zoomContact, exists := existing[clientIDStr]

	var req *http.Request

	if exists {
		// Update existing record
		endpoint := "https://api.zoom.us/v2/phone/external_contacts/" + url.PathEscape(zoomContact.ExternalContactID)
		req, err = http.NewRequest("PATCH", endpoint, bytes.NewReader(jsonPayload))
	} else {
		// Create new record
		req, err = http.NewRequest("POST", "https://api.zoom.us/v2/phone/external_contacts", bytes.NewReader(jsonPayload))
	}

	if err != nil {
		return fmt.Errorf("client %s: %w", clientIDStr, err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("client %s HTTP request: %w", clientIDStr, err)
	}
	defer resp.Body.Close()

	action := "CREATE"
	if exists {
		action = "UPDATE"
	}

	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated || resp.StatusCode == http.StatusNoContent {
		log.Printf("[%s OK] Client ID %s synced successfully.\n", action, clientIDStr)
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	return fmt.Errorf("%s client %s (%d): %s", action, clientIDStr, resp.StatusCode, body)
}

// --- UTILITY FUNCTIONS ---

func loadEnv(filename string) error {
	file, err := os.Open(filename)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, found := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if !found || key == "" {
			return fmt.Errorf("line %d: expected KEY=VALUE", lineNumber)
		}
		if len(value) >= 2 && (value[0] == '"' || value[0] == '\'') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		}
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("line %d: %w", lineNumber, err)
		}
	}

	return scanner.Err()
}

func formatE164(rawPhone string) string {
	number, err := phonenumbers.Parse(rawPhone, "AU")
	if err != nil || !phonenumbers.IsValidNumber(number) {
		return ""
	}
	return phonenumbers.Format(number, phonenumbers.E164)
}
