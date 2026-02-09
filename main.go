// main.go
package main

import (
	"bytes"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"unicode"
)

// Version is the application version, set at build time via ldflags
var Version = "dev"

//go:embed templates/*
var templatesFS embed.FS

//go:embed static/*
var staticFS embed.FS

const defaultBaseDomain = "http://metadata.google.internal"

type Metadata struct {
	Key           string
	KeyCorrected  string
	Path          string
	PathCorrected string
	Depth         int
	IsTerminal    bool
	IsIdentity    bool
	IsToken       bool
	Value         string
}

type Content struct {
	Segments             []Segment
	StandardContent      string
	StandardUrl          string
	JsonContent          string
	JsonUrl              string
	RecursiveContent     string
	RecursiveUrl         string
	RecursiveJsonContent string
	RecursiveJsonUrl     string
}

type ContentToken struct {
	Segments []Segment
	Token    string
	TokenUrl string
}

type ContentIdentity struct {
	Segments    []Segment
	GenerateUrl string
}

type Segment struct {
	Url  string
	Name string
	Link bool
}

var (
	baseDomain string
	authHeader string
	templates  *template.Template
)

func main() {
	// Set the build version from the build info if not set by the build system
	if Version == "dev" || Version == "" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			if bi.Main.Version != "" && bi.Main.Version != "(devel)" {
				Version = bi.Main.Version
			}
		}
	}

	// Environment variable to allow access to GCP Access and Identity tokens, which are disabled by default
	allowAccessTokens := os.Getenv("ALLOW_TOKENS") == "true"

	// Load environment variables
	baseDomain = os.Getenv("METADATA_BASE_URL")
	if baseDomain == "" {
		baseDomain = defaultBaseDomain
	}

	// Basic authentication used only for testing purposes
	// TODO: Remove this
	username := os.Getenv("METADATA_PROXY_USERNAME")
	password := os.Getenv("METADATA_PROXY_PASSWORD")
	if username != "" && password != "" {
		authHeader = "Basic " + basicAuth(username, password)
	}

	// Log version at startup
	log.Printf("Starting gcpmetadataexplorer version %s", Version)

	// Parse template with helper functions from embedded filesystem
	var err error
	templates, err = template.New("").Funcs(template.FuncMap{
		"multiply": multiply,
		"version":  func() string { return Version },
	}).ParseFS(templatesFS,
		"templates/index.html",
		"templates/content.html",
		"templates/error.html",
		"templates/token.html",
		"templates/tokenDisabled.html",
		"templates/identity.html",
		"templates/identityDisabled.html",
		"templates/identityToken.html")
	if err != nil {
		log.Fatalf("Error parsing templates: %v", err)
	}

	// Serve static files from the embedded "static" directory
	staticSubFS, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatalf("Error creating static sub-filesystem: %v", err)
	}
	fs := http.FileServer(http.FS(staticSubFS))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	// Root handler: fetch metadata and render the main HTML page
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {

		if r.URL.Path != "/" {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		var flattenedMetadata interface{}
		var errorMessage string

		// Fetch metadata
		stringBody, _, err := fetchMetadata("/", map[string]string{"alt": "json", "recursive": "true"})
		if err != nil {
			log.Println("Error fetching metadata:", err)
			errorMessage = "Failed to fetch metadata from the metadata server"
		} else {
			// Decode JSON
			var data interface{}
			decoder := json.NewDecoder(bytes.NewReader([]byte(stringBody)))
			decoder.UseNumber() // Preserve numbers as json.Number
			if err := decoder.Decode(&data); err != nil {
				log.Println("Error decoding metadata response:", err)
				errorMessage = "Failed to decode metadata response"
			} else {
				// Flatten the metadata
				flattenedMetadata = flattenMetadata(data)
			}
		}

		// Create a data object to pass to the template
		dataObj := map[string]interface{}{
			"FlattenedMetadata": flattenedMetadata,
			"Error":             errorMessage,
		}

		// Render the template with the flattened data
		if err := templates.ExecuteTemplate(w, "index.html", dataObj); err != nil {
			http.Error(w, fmt.Sprintf("Failed to render template: %v", err), http.StatusInternalServerError)
			log.Println("Error rendering template:", err)
		}
	})

	// Handler for HTMX requests to dynamically load token details
	http.HandleFunc("/token/", func(w http.ResponseWriter, r *http.Request) {
		// Extract the path from the request
		path := "/" + strings.TrimPrefix(r.URL.Path, "/token/")

		if !isTokenPath(path) {
			http.Error(w, "Invalid path", http.StatusNotFound)
			log.Println("Invalid path:", path)
			return
		}

		// Get the token content
		tokenContent, tokenURL, err := fetchMetadata(path, map[string]string{})
		if err != nil {
			renderError(w, "Failed to fetch token from the metadata server")
			log.Println("Error fetching metadata:", err)
			return
		}

		segments := buildBreadcrumb(tokenURL)

		dataObj := ContentToken{
			Segments: segments,
			Token:    tokenContent,
			TokenUrl: defaultBaseDomain + tokenURL,
		}

		if !allowAccessTokens {
			// Access token will not be displayed to the user
			if err := templates.ExecuteTemplate(w, "tokenDisabled.html", dataObj); err != nil {
				http.Error(w, fmt.Sprintf("Failed to render template: %v", err), http.StatusInternalServerError)
				log.Println("Error rendering template:", err)
			}
			return
		}

		// Return the token.html template
		if err := templates.ExecuteTemplate(w, "token.html", dataObj); err != nil {
			http.Error(w, fmt.Sprintf("Failed to render template: %v", err), http.StatusInternalServerError)
			log.Println("Error rendering template:", err)
		}
	})

	// Handler for HTMX requests to dynamically load identity details
	http.HandleFunc("/identity/", func(w http.ResponseWriter, r *http.Request) {
		// Extract the path from the request
		path := "/" + strings.TrimPrefix(r.URL.Path, "/identity/")

		if !isIdentityPath(path) {
			http.Error(w, "Invalid path", http.StatusNotFound)
			log.Println("Invalid path:", path)
			return
		}

		segments := buildBreadcrumb(path)

		dataObj := ContentIdentity{
			Segments:    segments,
			GenerateUrl: "/generateIdentity/" + path,
		}

		if !allowAccessTokens {
			// Access token will not be displayed to the user
			if err := templates.ExecuteTemplate(w, "identityDisabled.html", dataObj); err != nil {
				http.Error(w, fmt.Sprintf("Failed to render template: %v", err), http.StatusInternalServerError)
				log.Println("Error rendering template:", err)
			}
			return
		}

		// Return the identity.html template
		if err := templates.ExecuteTemplate(w, "identity.html", dataObj); err != nil {
			http.Error(w, fmt.Sprintf("Failed to render template: %v", err), http.StatusInternalServerError)
			log.Println("Error rendering template:", err)
		}
	})

	// Handler for HTMX requests to dynamically generate identity token
	http.HandleFunc("/generateIdentity/", func(w http.ResponseWriter, r *http.Request) {

		// Extract the path from the request
		path := "/" + strings.TrimPrefix(r.URL.Path, "/generateIdentity/")

		if !isIdentityPath(path) {
			http.Error(w, "Invalid path", http.StatusNotFound)
			log.Println("Invalid path:", path)
			return
		}

		if !allowAccessTokens {
			renderError(w, "Failed to generate identity token: access tokens are disabled")
			log.Println("Access tokens are disabled")
			return
		}

		// Get the query parameter "audience"
		audience := r.URL.Query().Get("audience")
		if audience == "" {
			renderError(w, "Failed to generate identity token: audience query parameter is required")
			log.Println("audience query parameter is required")
			return
		}

		content, tokenURL, err := fetchMetadata(path, map[string]string{"audience": audience})
		if err != nil {
			renderError(w, "Failed to generate identity token: failed to fetch metadata from the metadata server")
			log.Println("Error fetching metadata:", err)
			return
		}

		dataObj := ContentToken{
			Token:    content,
			TokenUrl: defaultBaseDomain + tokenURL,
		}

		// Return the identity.html template
		if err := templates.ExecuteTemplate(w, "identityToken.html", dataObj); err != nil {
			http.Error(w, fmt.Sprintf("Failed to render template: %v", err), http.StatusInternalServerError)
			log.Println("Error rendering template:", err)
		}
	})

	// Handler for HTMX requests to dynamically load metadata details
	http.HandleFunc("/metadata/", func(w http.ResponseWriter, r *http.Request) {
		// Extract the path from the request
		path := "/" + strings.TrimPrefix(r.URL.Path, "/metadata/")

		// Fetch metadata for the specified path

		if isIdentityPath(path) || isTokenPath(path) {
			// return an error
			http.Error(w, "Invalid path", http.StatusNotFound)
			log.Println("Invalid path:", path)
			return
		}

		standardContent, standardURL, err := fetchMetadata(path, map[string]string{})
		if err != nil {
			renderError(w, "Failed to fetch metadata from the metadata server")
			log.Println("Error fetching metadata:", err)
			return
		}

		jsonContent, jsonURL, err := fetchMetadata(path, map[string]string{"alt": "json"})
		if err != nil {
			renderError(w, "Failed to fetch metadata from the metadata server")
			log.Println("Error fetching metadata:", err)
			return
		}

		recursiveContent, recursiveURL, err := fetchMetadata(path, map[string]string{"recursive": "true"})
		if err != nil {
			renderError(w, "Failed to fetch metadata from the metadata server")
			log.Println("Error fetching metadata:", err)
			return
		}

		recursiveJsonContent, recursiveJsonURL, err := fetchMetadata(path, map[string]string{"alt": "json", "recursive": "true"})
		if err != nil {
			renderError(w, "Failed to fetch metadata from the metadata server")
			log.Println("Error fetching metadata:", err)
			return
		}

		segments := buildBreadcrumb(standardURL)

		dataObj := Content{
			Segments:             segments,
			StandardContent:      standardContent,
			StandardUrl:          defaultBaseDomain + standardURL,
			JsonContent:          jsonContent,
			JsonUrl:              defaultBaseDomain + jsonURL,
			RecursiveContent:     recursiveContent,
			RecursiveUrl:         defaultBaseDomain + recursiveURL,
			RecursiveJsonContent: recursiveJsonContent,
			RecursiveJsonUrl:     defaultBaseDomain + recursiveJsonURL,
		}

		// Render the template with the flattened data
		if err := templates.ExecuteTemplate(w, "content.html", dataObj); err != nil {
			http.Error(w, fmt.Sprintf("Failed to render template: %v", err), http.StatusInternalServerError)
			log.Println("Error rendering template:", err)
		}
	})

	// Start the server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	log.Printf("Server is running on port %s...", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// fetchMetadata retrieves data from the metadata service
func fetchMetadata(path string, attributes map[string]string) (string, string, error) {
	// Build the URL with query parameters from the attributes map
	baseURL, err := url.Parse(baseDomain)
	if err != nil {
		return "", "", fmt.Errorf("invalid base domain: %v", err)
	}

	// Append the path to the base URL
	baseURL.Path = path

	query := baseURL.Query()
	for key, value := range attributes {
		// Add these to query parameters
		query.Add(key, value)
	}

	baseURL.RawQuery = query.Encode()

	requestURL := baseURL.String()

	// Remove the host part from the request URL returned by the function
	requestPath := strings.TrimPrefix(requestURL, baseDomain)

	req, err := http.NewRequest("GET", requestURL, nil)
	if err != nil {
		return "", "", err
	}

	// Add headers
	req.Header.Set("Metadata-Flavor", "Google")
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}

	// Make the request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("received status %d from metadata service", resp.StatusCode)
	}

	// Convert the resp.Body to a string
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", fmt.Errorf("failed to read metadata response: %v", err)
	}

	return string(body), requestPath, nil
}

func buildBreadcrumb(path string) []Segment {
	pathSegments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	segments := make([]Segment, 0, len(pathSegments))
	for i := 0; i < len(pathSegments); i++ {
		segment := strings.Join(pathSegments[:i+1], "/")
		segments = append(segments, Segment{
			Url:  segment,
			Name: pathSegments[i],
			Link: i != len(pathSegments)-1,
		})
	}
	return segments
}

func renderError(w http.ResponseWriter, message string) {
	dataObj := map[string]interface{}{
		"Error": message,
	}

	if err := templates.ExecuteTemplate(w, "error.html", dataObj); err != nil {
		http.Error(w, fmt.Sprintf("Failed to render template: %v", err), http.StatusInternalServerError)
		log.Println("Error rendering template:", err)
	}
}

// basicAuth encodes the username and password for Basic Authentication.
func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

func multiply(a, b int) int {
	return a * b
}

// flattenMetadata recursively traverses the JSON data and flattens it into a slice of Metadata.
func flattenMetadata(data interface{}) []Metadata {
	var flattenedMetadata []Metadata
	flattenMetadataHelper(data, "", 0, &flattenedMetadata)
	return flattenedMetadata
}

// flattenMetadataHelper is a helper function that performs the recursive traversal.
func flattenMetadataHelper(data interface{}, path string, depth int, flattenedMetadata *[]Metadata) {
	switch v := data.(type) {
	case map[string]interface{}:

		// Collect and sort keys
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)

		for _, key := range keys {
			value := v[key]
			currentPath := fmt.Sprintf("%s/%s", path, key)
			// Remove leading slash if present
			currentPath = strings.TrimPrefix(currentPath, "/")
			isTerminal := !isMap(value) && !isArray(value)
			if isArray(value) {
				isTerminal = isTerminalArray(value.([]interface{}))
			}
			var valueStr string
			if isTerminal {
				valueStr = getValueString(value)
			}

			*flattenedMetadata = append(*flattenedMetadata, Metadata{
				Key:           key,
				KeyCorrected:  convertToKebabCase(key),
				Path:          currentPath,
				PathCorrected: convertToKebabCase(currentPath),
				Depth:         depth,
				IsTerminal:    isTerminal,
				Value:         valueStr,
			})

			// Inject special cases for service accounts
			if strings.HasPrefix(currentPath, "computeMetadata/v1/instance/serviceAccounts") && depth == 4 {
				injectSpecialCases(currentPath, depth+1, flattenedMetadata)
			}

			if !isTerminal {
				flattenMetadataHelper(value, currentPath, depth+1, flattenedMetadata)
			}
		}
	case []interface{}:
		if isTerminalArray(v) {
			// Serialize array to string
			valueStr, err := json.Marshal(v)
			if err != nil {
				valueStr = []byte(fmt.Sprintf("%v", v))
			}
			key := getLastSegment(path)
			*flattenedMetadata = append(*flattenedMetadata, Metadata{
				Key:        key,
				Path:       path,
				Depth:      depth,
				IsTerminal: true,
				Value:      string(valueStr),
			})
		} else {
			// Traverse into array elements
			for index, item := range v {
				key := fmt.Sprintf("[%d]", index)
				currentPath := fmt.Sprintf("%s/%d", path, index)
				currentPath = strings.TrimPrefix(currentPath, "/")
				*flattenedMetadata = append(*flattenedMetadata, Metadata{
					Key:        key,
					Path:       currentPath,
					Depth:      depth,
					IsTerminal: false,
					Value:      "",
				})
				flattenMetadataHelper(item, currentPath, depth+1, flattenedMetadata)
			}
		}
	default:
		// Primitive types (string, number, boolean, etc.)
		*flattenedMetadata = append(*flattenedMetadata, Metadata{
			Key:        fmt.Sprintf("%v", v),
			Path:       path,
			Depth:      depth,
			IsTerminal: true,
			Value:      fmt.Sprintf("%v", v),
		})
	}
}

// Helper function to inject `/token` and `/identity`
func injectSpecialCases(serviceAccountPath string, depth int, flattenedMetadata *[]Metadata) {

	*flattenedMetadata = append(*flattenedMetadata, Metadata{
		Key:           "token",
		KeyCorrected:  convertToKebabCase("token"),
		Path:          fmt.Sprintf("%s/%s", serviceAccountPath, "token"),
		PathCorrected: convertToKebabCase(fmt.Sprintf("%s/%s", serviceAccountPath, "token")),
		Depth:         depth,
		IsTerminal:    true,
		IsToken:       true,
	})

	*flattenedMetadata = append(*flattenedMetadata, Metadata{
		Key:           "identity",
		KeyCorrected:  convertToKebabCase("identity"),
		Path:          fmt.Sprintf("%s/%s", serviceAccountPath, "identity"),
		PathCorrected: convertToKebabCase(fmt.Sprintf("%s/%s", serviceAccountPath, "identity")),
		Depth:         depth,
		IsTerminal:    true,
		IsIdentity:    true,
	})
}

// isMap checks if the value is a map.
func isMap(value interface{}) bool {
	_, ok := value.(map[string]interface{})
	return ok
}

// isArray checks if the value is a slice.
func isArray(value interface{}) bool {
	_, ok := value.([]interface{})
	return ok
}

// isTerminalArray determines if an array consists solely of primitive types.
func isTerminalArray(arr []interface{}) bool {
	for _, item := range arr {
		switch item.(type) {
		case map[string]interface{}, []interface{}:
			return false
		}
	}
	return true
}

// getLastSegment retrieves the last segment from the path.
func getLastSegment(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}

// getValueString converts the value to a string representation.
func getValueString(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	case bool:
		return fmt.Sprintf("%v", v)
	case []interface{}:
		// Serialize the array to a JSON string
		arrBytes, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(arrBytes)
	case map[string]interface{}:
		// Serialize the map to a JSON string
		mapBytes, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(mapBytes)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// Add a new helper function to convert camelCase to kebab-case
func convertToKebabCase(s string) string {
	const prefix = "computeMetadata"
	if strings.HasPrefix(s, prefix) {
		rest := s[len(prefix):]
		var result strings.Builder
		for i, char := range rest {
			if i > 0 && unicode.IsUpper(char) {
				result.WriteRune('-')
			}
			result.WriteRune(unicode.ToLower(char))
		}
		return prefix + result.String()
	}
	var result strings.Builder
	for i, char := range s {
		if i > 0 && unicode.IsUpper(char) {
			result.WriteRune('-')
		}
		result.WriteRune(unicode.ToLower(char))
	}
	return result.String()
}

func isIdentityPath(path string) bool {
	matched, err := regexp.MatchString(`^/computeMetadata/v1/instance/service-accounts/[^/]+/identity$`, path)
	return err == nil && matched
}

func isTokenPath(path string) bool {
	matched, err := regexp.MatchString(`^/computeMetadata/v1/instance/service-accounts/[^/]+/token$`, path)
	return err == nil && matched
}
