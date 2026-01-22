// ucpclient is a CLI tool for testing UCP checkout flows.
// Each command performs a single operation, making it composable for scripts.
//
// Commands:
//
//	ucpclient create -proxy URL -product ID [-qty N]
//	ucpclient get -proxy URL -id <checkout-id>
//	ucpclient update -proxy URL -id <checkout-id> [-buyer] [-shipping] [-fulfillment ID] [-discount CODE]
//	ucpclient complete -proxy URL -id <checkout-id> -payment TYPE
//
// Examples:
//
//	ID=$(ucpclient create -proxy http://localhost:8080 -product 60 -q)
//	ucpclient update -proxy http://localhost:8080 -id $ID -buyer -shipping
//	ucpclient update -proxy http://localhost:8080 -id $ID -fulfillment flat_rate:1
//	ucpclient update -proxy http://localhost:8080 -id $ID -discount 10OFF
//	ucpclient complete -proxy http://localhost:8080 -id $ID -payment stripe
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

var client = &http.Client{Timeout: 30 * time.Second}

// Global flags (apply to all commands)
var (
	proxyURL    string
	quiet       bool
	noColor     bool
	verbose     bool
	profilePort int    // Port to serve agent profile on (0 = disabled)
	profileFile string // Path to agent profile JSON file
	profileURL  string // Computed profile URL (set when server starts)
)

// Profile server management
var (
	profileServer   *http.Server
	profileServerMu sync.Mutex
)

// ANSI color codes
var (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	colorCyan   = "\033[36m"
	colorGray   = "\033[90m"
	colorBold   = "\033[1m"
)

func init() {
	if os.Getenv("NO_COLOR") != "" {
		disableColors()
	}
}

func disableColors() {
	colorReset, colorRed, colorGreen, colorYellow = "", "", "", ""
	colorBlue, colorCyan, colorGray, colorBold = "", "", "", ""
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "create":
		runCreate(args)
	case "get":
		runGet(args)
	case "update":
		runUpdate(args)
	case "complete":
		runComplete(args)
	case "-h", "-help", "--help", "help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmd)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `ucpclient - UCP checkout flow test tool

Usage:
  ucpclient <command> [options]

Commands:
  create    Create a new checkout with line items
  get       Get current checkout state
  update    Update checkout (buyer, address, shipping, discount)
  complete  Complete checkout with payment

Examples:
  # Create checkout and capture ID
  ID=$(ucpclient create -proxy http://localhost:8080 -product 60 -q)

  # Add buyer info and shipping address
  ucpclient update -proxy http://localhost:8080 -id "$ID" -buyer -shipping

  # Select shipping method
  ucpclient update -proxy http://localhost:8080 -id "$ID" -fulfillment flat_rate:1

  # Apply discount
  ucpclient update -proxy http://localhost:8080 -id "$ID" -discount 10OFF

  # Complete with payment
  ucpclient complete -proxy http://localhost:8080 -id "$ID" -payment stripe

Run 'ucpclient <command> -h' for command-specific options.
`)
}

// =============================================================================
// PROFILE SERVER
// =============================================================================

// startProfileServer starts an HTTP server to serve the agent profile.
// Returns the profile URL that should be sent in UCP-Agent header.
func startProfileServer(port int, profilePath string) (string, error) {
	profileServerMu.Lock()
	defer profileServerMu.Unlock()

	// Read profile file
	profileData, err := os.ReadFile(profilePath)
	if err != nil {
		return "", fmt.Errorf("reading profile file: %w", err)
	}

	// Validate it's valid JSON
	var profile map[string]interface{}
	if err := json.Unmarshal(profileData, &profile); err != nil {
		return "", fmt.Errorf("invalid profile JSON: %w", err)
	}

	// Create server
	mux := http.NewServeMux()
	mux.HandleFunc("/profile", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=300") // 5 min cache
		w.Write(profileData)
	})

	// Find available port if port is 0
	listener, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", port))
	if err != nil {
		return "", fmt.Errorf("starting profile server: %w", err)
	}

	actualPort := listener.Addr().(*net.TCPAddr).Port
	profURL := fmt.Sprintf("http://localhost:%d/profile", actualPort)

	profileServer = &http.Server{Handler: mux}

	// Start server in goroutine
	go func() {
		if err := profileServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "Profile server error: %v\n", err)
		}
	}()

	return profURL, nil
}

// stopProfileServer gracefully shuts down the profile server.
func stopProfileServer() {
	profileServerMu.Lock()
	defer profileServerMu.Unlock()

	if profileServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		profileServer.Shutdown(ctx)
		profileServer = nil
	}
}

// defaultProfileJSON returns a minimal default agent profile for testing.
func defaultProfileJSON() []byte {
	profile := map[string]interface{}{
		"ucp": map[string]interface{}{
			"version": "2026-01-11",
			"capabilities": map[string]interface{}{
				"dev.ucp.shopping.checkout": []map[string]interface{}{
					{"version": "2026-01-11"},
				},
				"dev.ucp.shopping.discount": []map[string]interface{}{
					{"version": "2026-01-11"},
				},
				"dev.ucp.shopping.fulfillment": []map[string]interface{}{
					{"version": "2026-01-11"},
				},
			},
			"payment_handlers": map[string]interface{}{
				"com.google.pay": []map[string]interface{}{
					{"id": "gpay-checkout", "version": "2026-01-11"},
				},
				"com.braintreepayments": []map[string]interface{}{
					{"id": "braintree-card", "version": "2026-01-11"},
				},
			},
		},
	}
	data, _ := json.MarshalIndent(profile, "", "  ")
	return data
}

// ensureProfileServer starts the profile server if not already running.
// Uses profileFile if set, otherwise creates a temp file with default profile.
func ensureProfileServer() error {
	profileServerMu.Lock()
	if profileServer != nil {
		profileServerMu.Unlock()
		return nil // Already running
	}
	profileServerMu.Unlock()

	// Determine profile file to use
	profPath := profileFile
	if profPath == "" {
		// Create temp file with default profile
		tmpFile, err := os.CreateTemp("", "ucp-agent-profile-*.json")
		if err != nil {
			return fmt.Errorf("creating temp profile: %w", err)
		}
		if _, err := tmpFile.Write(defaultProfileJSON()); err != nil {
			tmpFile.Close()
			return fmt.Errorf("writing temp profile: %w", err)
		}
		tmpFile.Close()
		profPath = tmpFile.Name()
	}

	// Start server
	url, err := startProfileServer(profilePort, profPath)
	if err != nil {
		return err
	}
	profileURL = url

	if !quiet {
		printInfo("Profile server started at %s", profileURL)
	}
	return nil
}

// =============================================================================
// CREATE COMMAND
// =============================================================================

func runCreate(args []string) {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	fs.StringVar(&proxyURL, "proxy", "http://localhost:8080", "UCP proxy base URL")
	var productID string
	var quantity int
	fs.StringVar(&productID, "product", "", "Product ID (required)")
	fs.IntVar(&quantity, "qty", 1, "Quantity")
	fs.BoolVar(&quiet, "q", false, "Quiet mode - only output checkout ID")
	fs.BoolVar(&noColor, "no-color", false, "Disable colored output")
	fs.BoolVar(&verbose, "v", false, "Verbose - show full request/response")
	fs.IntVar(&profilePort, "profile-port", 0, "Port to serve agent profile (0=auto-select)")
	fs.StringVar(&profileFile, "profile-file", "", "Path to agent profile JSON (uses default if not set)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: ucpclient create -product ID [options]\n\nOptions:\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if noColor {
		disableColors()
	}

	// Start profile server if needed
	if err := ensureProfileServer(); err != nil {
		fatal("Failed to start profile server: %v", err)
	}
	defer stopProfileServer()

	if productID == "" {
		fs.Usage()
		os.Exit(1)
	}

	reqBody := map[string]interface{}{
		"line_items": []map[string]interface{}{
			{"product_id": productID, "quantity": quantity},
		},
	}

	resp, err := doRequest("POST", "/checkout-sessions", reqBody)
	if err != nil {
		fatal("Failed to create checkout: %v", err)
	}

	printResponseMessages(resp)

	checkoutID, _ := resp["id"].(string)
	if quiet {
		fmt.Println(checkoutID)
	} else {
		printSuccess("Checkout created")
		fmt.Printf("  ID: %s%s%s\n", colorCyan, checkoutID, colorReset)
	}
}

// =============================================================================
// GET COMMAND
// =============================================================================

func runGet(args []string) {
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	fs.StringVar(&proxyURL, "proxy", "http://localhost:8080", "UCP proxy base URL")
	var checkoutID string
	fs.StringVar(&checkoutID, "id", "", "Checkout ID (required)")
	fs.BoolVar(&quiet, "q", false, "Quiet mode - only output status")
	fs.BoolVar(&noColor, "no-color", false, "Disable colored output")
	fs.BoolVar(&verbose, "v", false, "Verbose - show full request/response")
	fs.IntVar(&profilePort, "profile-port", 0, "Port to serve agent profile (0=auto-select)")
	fs.StringVar(&profileFile, "profile-file", "", "Path to agent profile JSON (uses default if not set)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: ucpclient get -id <checkout-id> [options]\n\nOptions:\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if noColor {
		disableColors()
	}

	// Start profile server if needed
	if err := ensureProfileServer(); err != nil {
		fatal("Failed to start profile server: %v", err)
	}
	defer stopProfileServer()

	if checkoutID == "" {
		fs.Usage()
		os.Exit(1)
	}

	resp, err := doRequest("GET", "/checkout-sessions/"+url.PathEscape(checkoutID), nil)
	if err != nil {
		fatal("Failed to get checkout: %v", err)
	}

	printResponseMessages(resp)

	status, _ := resp["status"].(string)
	if quiet {
		fmt.Println(status)
	} else {
		printSuccess("Checkout retrieved")
		fmt.Printf("  Status: %s%s%s\n", colorCyan, status, colorReset)

		// Show fulfillment options if available
		if options, ok := resp["fulfillment_options"].([]interface{}); ok && len(options) > 0 {
			fmt.Printf("  %sFulfillment options:%s\n", colorYellow, colorReset)
			for _, opt := range options {
				if optMap, ok := opt.(map[string]interface{}); ok {
					fmt.Printf("    - %s: %s (%s)\n",
						optMap["id"], optMap["title"], formatCents(optMap["subtotal"]))
				}
			}
		}

		// Show totals
		if totals, ok := resp["totals"].(map[string]interface{}); ok {
			if total, ok := totals["total"].(float64); ok {
				fmt.Printf("  Total: %s%s%s\n", colorGreen, formatCents(total), colorReset)
			}
		}
	}
}

// =============================================================================
// UPDATE COMMAND
// =============================================================================

func runUpdate(args []string) {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	fs.StringVar(&proxyURL, "proxy", "http://localhost:8080", "UCP proxy base URL")

	var checkoutID string
	fs.StringVar(&checkoutID, "id", "", "Checkout ID (required)")

	// Update options - each triggers a specific update
	var addBuyer, addShipping, addBilling bool
	var fulfillmentID, discountCode, email string
	var removeDiscount bool

	fs.BoolVar(&addBuyer, "buyer", false, "Add test buyer info")
	fs.BoolVar(&addShipping, "shipping", false, "Add test shipping address")
	fs.BoolVar(&addBilling, "billing", false, "Add test billing address")
	fs.StringVar(&email, "email", "test@example.com", "Buyer email (used with -buyer)")
	fs.StringVar(&fulfillmentID, "fulfillment", "", "Select fulfillment option by ID")
	fs.StringVar(&discountCode, "discount", "", "Apply discount code")
	fs.BoolVar(&removeDiscount, "remove-discount", false, "Remove all discount codes")
	fs.BoolVar(&quiet, "q", false, "Quiet mode - only output status")
	fs.BoolVar(&noColor, "no-color", false, "Disable colored output")
	fs.BoolVar(&verbose, "v", false, "Verbose - show full request/response")
	fs.IntVar(&profilePort, "profile-port", 0, "Port to serve agent profile (0=auto-select)")
	fs.StringVar(&profileFile, "profile-file", "", "Path to agent profile JSON (uses default if not set)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: ucpclient update -id <checkout-id> [options]\n\n")
		fmt.Fprintf(os.Stderr, "At least one update option is required.\n")
		fmt.Fprintf(os.Stderr, "Uses PUT semantics: fetches current state, merges changes, sends full state.\n\nOptions:\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if noColor {
		disableColors()
	}

	// Start profile server if needed
	if err := ensureProfileServer(); err != nil {
		fatal("Failed to start profile server: %v", err)
	}
	defer stopProfileServer()

	if checkoutID == "" {
		fs.Usage()
		os.Exit(1)
	}

	hasUpdate := addBuyer || addShipping || addBilling || fulfillmentID != "" || discountCode != "" || removeDiscount
	if !hasUpdate {
		fmt.Fprintf(os.Stderr, "Error: at least one update option required\n\n")
		fs.Usage()
		os.Exit(1)
	}

	// Full PUT semantics: fetch current state first
	if !quiet {
		printInfo("Fetching current checkout state...")
	}
	current, err := doRequest("GET", "/checkout-sessions/"+url.PathEscape(checkoutID), nil)
	if err != nil {
		fatal("Failed to get checkout: %v", err)
	}

	// Extract current line_items and discount_codes from response
	lineItems := extractLineItems(current)
	discountCodes := extractDiscountCodes(current)

	// Build full PUT request body
	reqBody := make(map[string]interface{})

	// Required fields: always include current state
	reqBody["line_items"] = lineItems
	if discountCode != "" {
		// Add new discount to existing codes
		reqBody["discount_codes"] = append(discountCodes, discountCode)
	} else if removeDiscount {
		reqBody["discount_codes"] = []string{}
	} else {
		reqBody["discount_codes"] = discountCodes
	}

	// Optional fields: add if requested
	if addBuyer {
		reqBody["buyer"] = map[string]interface{}{
			"email":        email,
			"first_name":   "Test",
			"last_name":    "Buyer",
			"phone_number": "+14155551234",
		}
	}

	if addShipping {
		reqBody["shipping_address"] = map[string]interface{}{
			"first_name":       "Test",
			"last_name":        "Buyer",
			"street_address":   "150 Elgin Street",
			"address_locality": "Ottawa",
			"address_region":   "ON",
			"postal_code":      "K2P 1L4",
			"address_country":  "CA",
			"phone_number":     "+16135551234",
		}
	}

	if addBilling {
		reqBody["billing_address"] = map[string]interface{}{
			"first_name":       "Test",
			"last_name":        "Buyer",
			"street_address":   "150 Elgin Street",
			"address_locality": "Ottawa",
			"address_region":   "ON",
			"postal_code":      "K2P 1L4",
			"address_country":  "CA",
			"phone_number":     "+16135551234",
		}
	}

	if fulfillmentID != "" {
		reqBody["fulfillment_option_id"] = fulfillmentID
	}

	resp, err := doRequest("PUT", "/checkout-sessions/"+url.PathEscape(checkoutID), reqBody)
	if err != nil {
		fatal("Failed to update checkout: %v", err)
	}

	printResponseMessages(resp)

	status, _ := resp["status"].(string)
	if quiet {
		fmt.Println(status)
	} else {
		printSuccess("Checkout updated")
		fmt.Printf("  Status: %s%s%s\n", colorCyan, status, colorReset)

		// Show what was updated
		var updates []string
		if addBuyer {
			updates = append(updates, "buyer")
		}
		if addShipping {
			updates = append(updates, "shipping address")
		}
		if addBilling {
			updates = append(updates, "billing address")
		}
		if fulfillmentID != "" {
			updates = append(updates, "fulfillment: "+fulfillmentID)
		}
		if discountCode != "" {
			updates = append(updates, "discount: "+discountCode)
		}
		if removeDiscount {
			updates = append(updates, "removed discounts")
		}
		fmt.Printf("  Updated: %s\n", strings.Join(updates, ", "))
	}
}

// extractLineItems extracts line items from checkout response for PUT semantics.
// Converts response format to request format (product_id + quantity).
func extractLineItems(checkout map[string]interface{}) []map[string]interface{} {
	items, ok := checkout["line_items"].([]interface{})
	if !ok || len(items) == 0 {
		return []map[string]interface{}{}
	}

	result := make([]map[string]interface{}, 0, len(items))
	for _, item := range items {
		itemMap, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		// Extract item.item.id as product_id
		innerItem, ok := itemMap["item"].(map[string]interface{})
		if !ok {
			continue
		}
		productID, _ := innerItem["id"].(string)
		quantity, _ := itemMap["quantity"].(float64)

		if productID != "" && quantity > 0 {
			result = append(result, map[string]interface{}{
				"product_id": productID,
				"quantity":   int(quantity),
			})
		}
	}
	return result
}

// extractDiscountCodes extracts applied discount codes from checkout response.
func extractDiscountCodes(checkout map[string]interface{}) []string {
	discounts, ok := checkout["discounts"].(map[string]interface{})
	if !ok {
		return []string{}
	}

	codes, ok := discounts["codes"].([]interface{})
	if !ok {
		return []string{}
	}

	result := make([]string, 0, len(codes))
	for _, code := range codes {
		if s, ok := code.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

// =============================================================================
// COMPLETE COMMAND
// =============================================================================

func runComplete(args []string) {
	fs := flag.NewFlagSet("complete", flag.ExitOnError)
	fs.StringVar(&proxyURL, "proxy", "http://localhost:8080", "UCP proxy base URL")
	var checkoutID string
	fs.StringVar(&checkoutID, "id", "", "Checkout ID (required)")
	var paymentHandler string
	fs.StringVar(&paymentHandler, "payment", "", "Payment handler: stripe, braintree, redirect (required)")
	fs.BoolVar(&quiet, "q", false, "Quiet mode - only output result")
	fs.BoolVar(&noColor, "no-color", false, "Disable colored output")
	fs.BoolVar(&verbose, "v", false, "Verbose - show full request/response")
	fs.IntVar(&profilePort, "profile-port", 0, "Port to serve agent profile (0=auto-select)")
	fs.StringVar(&profileFile, "profile-file", "", "Path to agent profile JSON (uses default if not set)")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: ucpclient complete -id <checkout-id> -payment TYPE [options]\n\nOptions:\n")
		fs.PrintDefaults()
	}
	fs.Parse(args)

	if noColor {
		disableColors()
	}

	// Start profile server if needed
	if err := ensureProfileServer(); err != nil {
		fatal("Failed to start profile server: %v", err)
	}
	defer stopProfileServer()

	if checkoutID == "" || paymentHandler == "" {
		fs.Usage()
		os.Exit(1)
	}

	// Build payment instrument based on handler
	var handlerID, credType, credToken string
	switch paymentHandler {
	case "braintree":
		handlerID = "braintree.card"
		credType = "braintree.nonce"
		credToken = "fake-valid-nonce"
	case "redirect":
		handlerID = "redirect"
		credType = "redirect"
		credToken = "redirect"
	case "stripe":
		handlerID = "google.pay"
		credType = "stripe.payment_method"
		credToken = "pm_card_visa"
	default:
		fatal("Unknown payment handler: %s (use: stripe, braintree, redirect)", paymentHandler)
	}

	reqBody := map[string]interface{}{
		"payment": map[string]interface{}{
			"instruments": []map[string]interface{}{
				{
					"id":         "instrument_1",
					"handler_id": handlerID,
					"type":       "card",
					"selected":   true,
					"credential": map[string]interface{}{
						"type":  credType,
						"token": credToken,
					},
					"billing_address": map[string]interface{}{
						"first_name":       "Test",
						"last_name":        "Buyer",
						"street_address":   "150 Elgin Street",
						"address_locality": "Ottawa",
						"address_region":   "ON",
						"postal_code":      "K2P 1L4",
						"address_country":  "CA",
						"phone_number":     "+16135551234",
					},
				},
			},
		},
	}

	resp, err := doRequest("POST", "/checkout-sessions/"+url.PathEscape(checkoutID)+"/complete", reqBody)
	if err != nil {
		fatal("Failed to complete checkout: %v", err)
	}

	printResponseMessages(resp)

	status, _ := resp["status"].(string)
	if quiet {
		fmt.Println(status)
		if status == "requires_escalation" {
			if continueURL, ok := resp["continue_url"].(string); ok {
				fmt.Println(continueURL)
			}
		}
	} else {
		switch status {
		case "completed":
			printSuccess("Payment completed!")
			if orderID, ok := resp["order_id"].(string); ok {
				fmt.Printf("  Order ID: %s%s%s\n", colorGreen, orderID, colorReset)
			}
			if orderURL, ok := resp["order_permalink_url"].(string); ok {
				fmt.Printf("  Order URL: %s%s%s\n", colorBlue, orderURL, colorReset)
			}
		case "requires_escalation":
			printWarning("Requires escalation (3DS or redirect)")
			if continueURL, ok := resp["continue_url"].(string); ok {
				fmt.Printf("  Continue URL: %s%s%s\n", colorBlue, continueURL, colorReset)
			}
		default:
			printWarning("Status: %s", status)
		}
	}
}

// =============================================================================
// HTTP HELPERS
// =============================================================================

func doRequest(method, path string, body interface{}) (map[string]interface{}, error) {
	var reqBody io.Reader
	var reqJSON []byte

	if body != nil {
		var err error
		reqJSON, err = json.MarshalIndent(body, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("marshaling request: %w", err)
		}
		reqBody = bytes.NewReader(reqJSON)
	}

	reqURL := proxyURL + path
	req, err := http.NewRequest(method, reqURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Add UCP-Agent header with profile URL (required by spec)
	if profileURL != "" {
		req.Header.Set("UCP-Agent", fmt.Sprintf(`profile="%s"`, profileURL))
	}

	if !quiet {
		printRequest(method, path, reqJSON)
	}

	start := time.Now()
	resp, err := client.Do(req)
	duration := time.Since(start)

	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if !quiet {
		printResponse(resp.StatusCode, respBody, duration)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parsing response: %w", err)
	}

	return result, nil
}

// =============================================================================
// OUTPUT HELPERS
// =============================================================================

func printRequest(method, path string, body []byte) {
	fmt.Printf("\n%s▶ REQUEST%s %s%s %s%s\n", colorYellow, colorReset, colorBold, method, path, colorReset)
	if body != nil {
		printJSON(body, "  ")
	}
}

func printResponse(status int, body []byte, duration time.Duration) {
	statusColor := colorGreen
	if status >= 400 {
		statusColor = colorRed
	}
	fmt.Printf("\n%s◀ RESPONSE%s %s%d%s (%v)\n", colorCyan, colorReset, statusColor, status, colorReset, duration)
	printJSON(body, "  ")
}

func printJSON(data []byte, prefix string) {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, data, prefix, "  "); err != nil {
		fmt.Printf("%s%s\n", prefix, string(data))
		return
	}

	output := pretty.String()
	if !verbose {
		lines := strings.Split(output, "\n")
		if len(lines) > 30 {
			lines = append(lines[:25], fmt.Sprintf("%s  %s(%d more lines, use -v for full output)%s", prefix, colorGray, len(lines)-25, colorReset))
			output = strings.Join(lines, "\n")
		}
	}
	fmt.Println(output)
}

func printSuccess(format string, args ...interface{}) {
	if !quiet {
		fmt.Printf("%s✓ %s%s\n", colorGreen, fmt.Sprintf(format, args...), colorReset)
	}
}

func printError(format string, args ...interface{}) {
	fmt.Printf("%s✗ %s%s\n", colorRed, fmt.Sprintf(format, args...), colorReset)
}

func printWarning(format string, args ...interface{}) {
	fmt.Printf("%s⚠ %s%s\n", colorYellow, fmt.Sprintf(format, args...), colorReset)
}

func printInfo(format string, args ...interface{}) {
	if !quiet {
		fmt.Printf("%s→ %s%s\n", colorGray, fmt.Sprintf(format, args...), colorReset)
	}
}

func printResponseMessages(resp map[string]interface{}) {
	if quiet {
		return
	}
	messages, ok := resp["messages"].([]interface{})
	if !ok || len(messages) == 0 {
		return
	}

	for _, msg := range messages {
		msgMap, ok := msg.(map[string]interface{})
		if !ok {
			continue
		}
		msgType, _ := msgMap["type"].(string)
		content, _ := msgMap["content"].(string)
		code, _ := msgMap["code"].(string)

		text := content
		if text == "" && code != "" {
			text = code
		}
		if text == "" {
			continue
		}

		switch msgType {
		case "error":
			printError("%s", text)
		case "warning":
			printWarning("%s", text)
		default:
			fmt.Printf("%s  ℹ %s%s\n", colorGray, text, colorReset)
		}
	}
}

func formatCents(v interface{}) string {
	switch val := v.(type) {
	case float64:
		return fmt.Sprintf("$%.2f", val/100)
	case int:
		return fmt.Sprintf("$%.2f", float64(val)/100)
	default:
		return fmt.Sprintf("%v", v)
	}
}

func fatal(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "%s✗ %s%s\n", colorRed, fmt.Sprintf(format, args...), colorReset)
	os.Exit(1)
}
