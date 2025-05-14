package firebase

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"text/template"
	"time"

	fbclient "github.com/oatsaysai/billing-in-discord/pkg/firebase"
)

// DeployBillWebsite deploys a bill allocation website to Firebase Hosting
func DeployBillWebsite(client *fbclient.Client, token, merchantName string, items []map[string]interface{}, users []map[string]interface{}) (string, error) {
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
	contentDir, err := generateWebsiteHTML(token, merchantName, items, users)
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
func generateWebsiteHTML(token, merchantName string, items []map[string]interface{}, users []map[string]interface{}) (string, error) {
	// Create a temporary directory for the website files
	tempDir, err := os.MkdirTemp("", "bill-website-")
	if err != nil {
		return "", fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Define the HTML template
	htmlTemplate := `
<!DOCTYPE html>
<html lang="th">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>ระบบแบ่งบิล - {{.MerchantName}}</title>
    <link href="https://cdn.jsdelivr.net/npm/tailwindcss@2.2.19/dist/tailwind.min.css" rel="stylesheet">
</head>
<body class="bg-gray-100">
    <div class="container mx-auto px-4 py-8">
        <h1 class="text-2xl font-bold mb-4">ระบบแบ่งบิล - {{.MerchantName}}</h1>
        
        <div class="bg-white p-4 rounded shadow mb-6">
            <h2 class="text-xl font-semibold mb-2">รายการในบิล</h2>
            <div class="overflow-x-auto">
                <table class="min-w-full bg-white">
                    <thead>
                        <tr>
                            <th class="py-2 px-4 border-b border-gray-200 bg-gray-100 text-left text-xs font-semibold text-gray-600 uppercase tracking-wider">
                                ลำดับ
                            </th>
                            <th class="py-2 px-4 border-b border-gray-200 bg-gray-100 text-left text-xs font-semibold text-gray-600 uppercase tracking-wider">
                                รายการ
                            </th>
                            <th class="py-2 px-4 border-b border-gray-200 bg-gray-100 text-left text-xs font-semibold text-gray-600 uppercase tracking-wider">
                                จำนวน
                            </th>
                            <th class="py-2 px-4 border-b border-gray-200 bg-gray-100 text-left text-xs font-semibold text-gray-600 uppercase tracking-wider">
                                ราคา
                            </th>
                            <th class="py-2 px-4 border-b border-gray-200 bg-gray-100 text-left text-xs font-semibold text-gray-600 uppercase tracking-wider">
                                รวม
                            </th>
                            <th class="py-2 px-4 border-b border-gray-200 bg-gray-100 text-left text-xs font-semibold text-gray-600 uppercase tracking-wider">
                                ผู้ร่วมจ่าย
                            </th>
                        </tr>
                    </thead>
                    <tbody>
                        {{range $index, $item := .Items}}
                        <tr data-item-id="{{$item.id}}">
                            <td class="py-2 px-4 border-b border-gray-200">{{$item.id}}</td>
                            <td class="py-2 px-4 border-b border-gray-200">{{$item.name}}</td>
                            <td class="py-2 px-4 border-b border-gray-200">{{$item.quantity}}</td>
                            <td class="py-2 px-4 border-b border-gray-200">{{$item.price}}</td>
                            <td class="py-2 px-4 border-b border-gray-200">{{$item.total}}</td>
                            <td class="py-2 px-4 border-b border-gray-200">
                                <div class="flex flex-wrap gap-2 items-center">
                                    {{range $.Users}}
                                    <label class="inline-flex items-center">
                                        <input type="checkbox" class="form-checkbox h-4 w-4 text-blue-600 item-user-checkbox" 
                                               data-item-id="{{$item.id}}" 
                                               data-user-id="{{.id}}">
                                        <span class="ml-2 text-sm">{{.name}}</span>
                                    </label>
                                    {{end}}
                                </div>
                            </td>
                        </tr>
                        {{end}}
                    </tbody>
                </table>
            </div>
        </div>
        
        <div class="bg-white p-4 rounded shadow mb-6">
            <h2 class="text-xl font-semibold mb-2">รายการเพิ่มเติม</h2>
            <div id="additional-items" class="space-y-4">
                <!-- Additional items will be added here dynamically -->
                <div class="additional-item flex flex-wrap gap-2 items-center mb-4">
                    <input type="text" placeholder="รายการเพิ่มเติม" class="border p-2 rounded desc-input">
                    <input type="number" placeholder="จำนวนเงิน" class="border p-2 rounded amount-input" min="0" step="0.01">
                    
                    <div class="users-checkboxes flex flex-wrap gap-2 items-center">
                        {{range .Users}}
                        <label class="inline-flex items-center">
                            <input type="checkbox" class="form-checkbox h-4 w-4 text-blue-600 additional-user-checkbox"
                                   data-user-id="{{.id}}">
                            <span class="ml-2 text-sm">{{.name}}</span>
                        </label>
                        {{end}}
                    </div>
                </div>
            </div>
            <button id="add-item-btn" class="mt-2 px-4 py-2 bg-blue-500 text-white rounded hover:bg-blue-600">
                เพิ่มรายการ
            </button>
        </div>
        
        <div class="bg-white p-4 rounded shadow mb-6">
            <h2 class="text-xl font-semibold mb-2">PromptPay ID (ถ้ามี)</h2>
            <input type="text" id="promptpay-id" placeholder="ระบุ PromptPay ID" class="border p-2 rounded w-full">
        </div>
        
        <button id="submit-btn" class="w-full px-4 py-2 bg-green-500 text-white rounded hover:bg-green-600 mb-4">
            ส่งข้อมูล
        </button>
        
        <div id="loading" class="hidden">
            <div class="text-center py-4">
                <div class="spinner inline-block w-8 h-8 border-4 rounded-full border-t-blue-500 animate-spin"></div>
                <p class="mt-2">กำลังส่งข้อมูล...</p>
            </div>
        </div>
        
        <div id="success-message" class="hidden bg-green-100 border border-green-400 text-green-700 px-4 py-3 rounded">
            <p>ส่งข้อมูลสำเร็จแล้ว! กรุณาตรวจสอบใน Discord</p>
        </div>
        
        <div id="error-message" class="hidden bg-red-100 border border-red-400 text-red-700 px-4 py-3 rounded">
            <p>เกิดข้อผิดพลาดในการส่งข้อมูล</p>
            <p id="error-detail" class="text-sm"></p>
        </div>
    </div>
    
    <script>
        // Store the token for submission
        const token = "{{.Token}}";
        const webhookURL = "/api/bill-webhook";
        
        // Handle add more item button
        document.getElementById('add-item-btn').addEventListener('click', () => {
            const additionalItemsContainer = document.getElementById('additional-items');
            const itemTemplate = additionalItemsContainer.querySelector('.additional-item').cloneNode(true);
            
            // Clear inputs in the new item
            const inputs = itemTemplate.querySelectorAll('input');
            inputs.forEach(input => {
                if (input.type === 'text' || input.type === 'number') {
                    input.value = '';
                } else if (input.type === 'checkbox') {
                    input.checked = false;
                }
            });
            
            additionalItemsContainer.appendChild(itemTemplate);
        });
        
        // Handle form submission
        document.getElementById('submit-btn').addEventListener('click', async () => {
            // Show loading state
            document.getElementById('loading').classList.remove('hidden');
            document.getElementById('success-message').classList.add('hidden');
            document.getElementById('error-message').classList.add('hidden');
            
            try {
                // Collect item allocations
                const itemAllocations = {};
                const itemRows = document.querySelectorAll('tr[data-item-id]');
                
                itemRows.forEach(row => {
                    const itemId = row.dataset.itemId;
                    const checkedUsers = row.querySelectorAll('.item-user-checkbox:checked');
                    
                    if (checkedUsers.length > 0) {
                        itemAllocations[itemId] = [];
                        checkedUsers.forEach(checkbox => {
                            itemAllocations[itemId].push(checkbox.dataset.userId);
                        });
                    }
                });
                
                // Collect additional items
                const additionalItems = [];
                const additionalItemElements = document.querySelectorAll('.additional-item');
                
                additionalItemElements.forEach(element => {
                    const description = element.querySelector('.desc-input').value.trim();
                    const amount = parseFloat(element.querySelector('.amount-input').value);
                    const userCheckboxes = element.querySelectorAll('.additional-user-checkbox:checked');
                    
                    const users = [];
                    userCheckboxes.forEach(checkbox => {
                        users.push(checkbox.dataset.userId);
                    });
                    
                    if (description && !isNaN(amount) && amount > 0 && users.length > 0) {
                        additionalItems.push({
                            description,
                            amount,
                            users
                        });
                    }
                });
                
                // Get PromptPay ID
                const promptPayID = document.getElementById('promptpay-id').value.trim();
                
                // Prepare data for submission
                const data = {
                    token,
                    itemAllocations,
                    additionalItems,
                    promptPayID
                };
                
                // Submit data to the server
                const response = await fetch(webhookURL, {
                    method: 'POST',
                    headers: {
                        'Content-Type': 'application/json'
                    },
                    body: JSON.stringify(data)
                });
                
                // Hide loading state
                document.getElementById('loading').classList.add('hidden');
                
                if (response.ok) {
                    // Show success message
                    document.getElementById('success-message').classList.remove('hidden');
                    
                    // Disable submit button
                    document.getElementById('submit-btn').disabled = true;
                    document.getElementById('submit-btn').classList.add('bg-gray-400');
                    document.getElementById('submit-btn').classList.remove('bg-green-500', 'hover:bg-green-600');
                } else {
                    const errorData = await response.json();
                    throw new Error(errorData.message || 'การส่งข้อมูลล้มเหลว');
                }
            } catch (error) {
                // Hide loading state and show error
                document.getElementById('loading').classList.add('hidden');
                document.getElementById('error-message').classList.remove('hidden');
                document.getElementById('error-detail').textContent = error.message;
            }
        });
    </script>
</body>
</html>
`

	// Create template data
	data := struct {
		Token        string
		MerchantName string
		Items        []map[string]interface{}
		Users        []map[string]interface{}
	}{
		Token:        token,
		MerchantName: merchantName,
		Items:        items,
		Users:        users,
	}

	// Parse the template
	tmpl, err := template.New("website").Parse(htmlTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
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
