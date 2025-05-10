package firebase

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// Client represents a Firebase client
type Client struct {
	CLIPath        string
	ProjectID      string
	SiteNamePrefix string
}

// SiteCreateResult is the result of creating a Firebase site
type SiteCreateResult struct {
	Status string `json:"status"`
	Result struct {
		Name       string `json:"name"`
		DefaultURL string `json:"defaultUrl"`
		Type       string `json:"type"`
	} `json:"result"`
	Error struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

// DeployResult is the result of a Firebase deployment
type DeployResult struct {
	Status string                 `json:"status"`
	Result map[string]interface{} `json:"result"`
	Error  struct {
		Message string `json:"message"`
		Code    int    `json:"code"`
	} `json:"error"`
}

// NewClient creates a new Firebase client
func NewClient(cliPath, projectID, siteNamePrefix string) *Client {
	return &Client{
		CLIPath:        cliPath,
		ProjectID:      projectID,
		SiteNamePrefix: siteNamePrefix,
	}
}

// ValidateSiteName validates the site name
func (c *Client) ValidateSiteName(siteName string) error {
	// Checking if site name matches the pattern: lowercase letters, numbers, and hyphens
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,20}[a-z0-9]$`).MatchString(siteName) {
		return fmt.Errorf("site name must be 3-22 characters long, containing only lowercase letters, numbers, and hyphens. Cannot start or end with a hyphen")
	}
	
	// Ensure it has the correct prefix
	if !strings.HasPrefix(siteName, c.SiteNamePrefix) {
		return fmt.Errorf("site name must start with prefix: %s", c.SiteNamePrefix)
	}
	
	return nil
}

// CreateSite creates a new Firebase hosting site
func (c *Client) CreateSite(siteName string) (*SiteCreateResult, error) {
	// Validate site name
	if err := c.ValidateSiteName(siteName); err != nil {
		return nil, err
	}
	
	// Create the site using the Firebase CLI
	cmd := exec.Command(c.CLIPath, "hosting:sites:create", siteName, "--project", c.ProjectID, "--json")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to create Firebase site: %s (error: %w)", string(output), err)
	}
	
	// Extract JSON part if needed
	jsonPart := extractJSON(string(output))
	
	// Parse the result
	var result SiteCreateResult
	if err := json.Unmarshal([]byte(jsonPart), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Firebase CLI output: %w", err)
	}
	
	// Check for Firebase error
	if result.Error.Message != "" {
		return nil, fmt.Errorf("Firebase error (code %d): %s", result.Error.Code, result.Error.Message)
	}
	
	return &result, nil
}

// DeleteSite deletes a Firebase hosting site
func (c *Client) DeleteSite(siteName string) error {
	// Validate site name
	if err := c.ValidateSiteName(siteName); err != nil {
		return err
	}
	
	// Delete the site using the Firebase CLI
	cmd := exec.Command(c.CLIPath, "hosting:sites:delete", siteName, "--project", c.ProjectID, "--force")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to delete Firebase site: %s (error: %w)", string(output), err)
	}
	
	return nil
}

// DeploySite deploys content to a Firebase hosting site
func (c *Client) DeploySite(siteName, contentDir string) (*DeployResult, error) {
	// Validate site name
	if err := c.ValidateSiteName(siteName); err != nil {
		return nil, err
	}
	
	// Check if content directory exists
	if _, err := os.Stat(contentDir); os.IsNotExist(err) {
		return nil, fmt.Errorf("content directory does not exist: %s", contentDir)
	}
	
	// Create a temporary firebase.json file
	tmpDir, err := os.MkdirTemp("", "firebase-deploy-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tmpDir)
	
	firebaseConfig := fmt.Sprintf(`{
		"hosting": {
			"site": "%s",
			"public": "%s"
		}
	}`, siteName, contentDir)
	
	firebaseJsonPath := filepath.Join(tmpDir, "firebase.json")
	if err := os.WriteFile(firebaseJsonPath, []byte(firebaseConfig), 0644); err != nil {
		return nil, fmt.Errorf("failed to write firebase.json: %w", err)
	}
	
	// Deploy to Firebase
	cmd := exec.Command(c.CLIPath, "deploy", "--only", "hosting:"+siteName, "--project", c.ProjectID, "--json")
	cmd.Dir = tmpDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to deploy to Firebase: %s (error: %w)", string(output), err)
	}
	
	// Extract JSON part if needed
	jsonPart := extractJSON(string(output))
	
	// Parse the result
	var result DeployResult
	if err := json.Unmarshal([]byte(jsonPart), &result); err != nil {
		return nil, fmt.Errorf("failed to parse Firebase CLI output: %w", err)
	}
	
	// Check for Firebase error
	if result.Error.Message != "" {
		return nil, fmt.Errorf("Firebase error (code %d): %s", result.Error.Code, result.Error.Message)
	}
	
	return &result, nil
}

// Helper function to extract JSON from Firebase CLI output
func extractJSON(output string) string {
	// Firebase CLI sometimes outputs additional text before the JSON
	jsonRegex := regexp.MustCompile(`(?s)\{.*\}`)
	match := jsonRegex.FindString(output)
	if match != "" {
		return match
	}
	return output
}