package firebase

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"text/template"
	"time"

	fbclient "github.com/oatsaysai/billing-in-discord/pkg/firebase"
)

// DeployBillWebsite deploys a bill allocation website to Firebase Hosting
func DeployBillWebsite(client *fbclient.Client, token, merchantName, webhookURL string, items []map[string]interface{}, users []map[string]interface{}) (string, error) {
	// แก้ไขเช็คเงื่อนไข
	if client == nil {
		return "", fmt.Errorf("Firebase client is not provided")
	}

	// Generate a unique site name based on the token
	siteName := fmt.Sprintf("bill-%s", token[:12])

	// Validate the site name
	if err := client.ValidateSiteName(siteName); err != nil {
		// Use a more generic name if validation fails
		siteName = fmt.Sprintf("bill-app-%d", time.Now().Unix())
	}

	// Create the site
	siteResult, err := client.CreateSite(siteName)
	if err != nil {
		log.Printf("Error creating Firebase site: %v", err)
		return "", fmt.Errorf("failed to create Firebase site: %w", err)
	}

	// Generate the website HTML
	contentDir, err := generateWebsiteHTML(token, merchantName, webhookURL, items, users)
	if err != nil {
		log.Printf("Error generating website HTML: %v", err)
		return "", fmt.Errorf("failed to generate website HTML: %w", err)
	}

	// ตรวจสอบว่าไดเรกทอรีมีอยู่จริง
	if _, err := os.Stat(contentDir); os.IsNotExist(err) {
		return "", fmt.Errorf("website content directory does not exist: %s", contentDir)
	}

	// เก็บค่าเพื่อลบในภายหลัง แต่ไม่ใช้ defer
	tempDir := contentDir

	// Deploy the site
	deployResult, err := client.DeploySite(siteName, contentDir)

	// ลบไดเรกทอรีชั่วคราวหลังจาก deploy เสร็จสิ้น (ไม่ว่าจะสำเร็จหรือไม่)
	// ใช้ goroutine เพื่อให้แน่ใจว่าจะลบหลังจากที่ deploy เสร็จสิ้นแล้ว
	go func() {
		// รอสักครู่ก่อนที่จะลบไดเรกทอรี
		time.Sleep(2 * time.Second)
		os.RemoveAll(tempDir)
	}()

	if err != nil {
		log.Printf("Error deploying to Firebase: %v", err)
		return "", fmt.Errorf("failed to deploy to Firebase: %w", err)
	}

	// Get the deployed URL
	var websiteURL string
	if deployResult != nil && deployResult.Result != nil {
		// Try to extract the URL from the deployment result
		if hostsVal, exists := deployResult.Result["hosting"]; exists {
			if hosts, ok := hostsVal.([]interface{}); ok && len(hosts) > 0 {
				if host, ok := hosts[0].(map[string]interface{}); ok {
					if urlVal, exists := host["url"]; exists {
						if url, ok := urlVal.(string); ok {
							websiteURL = url
						}
					}
				}
			}
		}
	}

	// If we couldn't extract from the result, use the site's default URL
	if websiteURL == "" && siteResult != nil && siteResult.Result.DefaultURL != "" {
		websiteURL = siteResult.Result.DefaultURL
	}

	// Fallback to a constructed URL if needed
	if websiteURL == "" {
		websiteURL = fmt.Sprintf("https://%s.web.app/?token=%s", siteName, token)
	}

	log.Printf("Deployed bill website: token=%s, merchant=%s, URL=%s",
		token, merchantName, websiteURL)

	return websiteURL, nil
}

// generateWebsiteHTML generates the HTML for the bill allocation website
func generateWebsiteHTML(token, merchantName, webhookURL string, items []map[string]interface{}, users []map[string]interface{}) (string, error) {
	// Create a temporary directory for the website files
	tempDir, err := os.MkdirTemp("", "bill-website-")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Create template data
	data := struct {
		Token        string
		MerchantName string
		Items        []map[string]interface{}
		Users        []map[string]interface{}
		UsersJSON    string
		WebhookURL   string
	}{
		Token:        token,
		MerchantName: merchantName,
		Items:        items,
		Users:        users,
		UsersJSON:    toJSONString(users),
		WebhookURL:   webhookURL,
	}

	// Get the path to the HTML template file
	templatePath := filepath.Join("internal", "firebase", "templates", "bill_allocation.html")

	// Check if the template file exists
	if _, err := os.Stat(templatePath); os.IsNotExist(err) {
		// Look in the current directory
		cwd, _ := os.Getwd()
		log.Printf("Template not found at %s, current directory is %s", templatePath, cwd)

		// Try alternative paths
		altPaths := []string{
			"templates/bill_allocation.html",
			"../templates/bill_allocation.html",
			"internal/firebase/templates/bill_allocation.html",
			"../internal/firebase/templates/bill_allocation.html",
		}

		for _, path := range altPaths {
			if _, err := os.Stat(path); err == nil {
				templatePath = path
				log.Printf("Found template at %s", templatePath)
				break
			}
		}
	}

	// Parse the template from file
	tmpl, err := template.ParseFiles(templatePath)
	if err != nil {
		return "", fmt.Errorf("failed to parse template file %s: %w", templatePath, err)
	}

	// Create the output directory structure
	publicDir := filepath.Join(tempDir, "public")
	err = os.MkdirAll(publicDir, 0755)
	if err != nil {
		return "", fmt.Errorf("failed to create public directory: %w", err)
	}

	// Create the output file
	outputPath := filepath.Join(publicDir, "index.html")
	outputFile, err := os.Create(outputPath)
	if err != nil {
		return "", fmt.Errorf("failed to create output file: %w", err)
	}
	defer outputFile.Close()

	// Execute the template
	err = tmpl.Execute(outputFile, data)
	if err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return tempDir, nil
}

func toJSONString(v interface{}) string {
	jsonData, err := json.Marshal(v)
	if err != nil {
		log.Printf("Error marshaling to JSON: %v", err)
		return "[]" // Return empty array as fallback
	}
	return string(jsonData)
}
