// main.go
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"unicode"
)

// Metadata represents each flattened JSON node.
type Metadata struct {
	Key           string
	Path          string
	PathCorrected string
	Depth         int
	IsTerminal    bool
	Value         string
}

var (
	baseDomain string
	authHeader string
	templates  *template.Template
)

func main() {

	// Load environment variables
	baseDomain = os.Getenv("METADATA_BASE_URL")
	if baseDomain == "" {
		baseDomain = "http://metadata.google.internal"
	}

	// Basic authentication used only for testing purposes
	// TODO: Remove this
	username := os.Getenv("METADATA_PROXY_USERNAME")
	password := os.Getenv("METADATA_PROXY_PASSWORD")
	if username != "" && password != "" {
		authHeader = "Basic " + basicAuth(username, password)
	}

	// Parse template with helper functions
	var err error
	templates, err = template.New("").Funcs(template.FuncMap{
		"multiply": multiply,
	}).ParseFiles("templates/index.html")
	if err != nil {
		log.Fatalf("Error parsing templates: %v", err)
	}

	// Root handler: fetch metadata and render the main HTML page
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {

		// Fetch metadata
		data, stringBody, err := fetchMetadata("/", true, true) // Set setJson to true
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to fetch metadata: %v", err), http.StatusInternalServerError)
			log.Println("Error fetching metadata:", err)
			return
		}

		// Flatten the metadata
		flattenedMetadata := flattenMetadata(data)

		// Create a data object to pass to the template
		dataObj := map[string]interface{}{
			"Metadata":          stringBody,
			"FlattenedMetadata": flattenedMetadata,
		}

		// Render the template with the flattened data
		if err := templates.ExecuteTemplate(w, "index.html", dataObj); err != nil {
			http.Error(w, fmt.Sprintf("Failed to render template: %v", err), http.StatusInternalServerError)
			log.Println("Error rendering template:", err)
		}
	})

	// Handler for HTMX requests to dynamically load metadata details
	http.HandleFunc("/metadata/", func(w http.ResponseWriter, r *http.Request) {
		// Extract the path from the request
		path := strings.TrimPrefix(r.URL.Path, "/metadata/")

		// Fetch metadata for the specified path
		data, _, err := fetchMetadata("/"+path, false, true) // Only fetch the specific path
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to fetch metadata: %v", err), http.StatusInternalServerError)
			log.Println("Error fetching metadata:", err)
			return
		}

		// Render the metadata details as JSON for now (can be adjusted to HTML if needed)
		w.Header().Set("Content-Type", "application/json")
		jsonResponse, err := json.MarshalIndent(data, "", "  ")
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to serialize metadata: %v", err), http.StatusInternalServerError)
			log.Println("Error serializing metadata:", err)
			return
		}

		w.Write(jsonResponse)
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

// fetchMetadata retrieves JSON data from the metadata service or a local file for testing.
func fetchMetadata(path string, setRecursive bool, setJson bool) (interface{}, string, error) {
	// Build the URL, if recursive is true add "recursive=true" to the query string if json is true add "alt=json" to the query string
	url := fmt.Sprintf("%s%s?", baseDomain, path)
	if setRecursive {
		url += "recursive=true&"
	}
	if setJson {
		url += "alt=json&"
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, "", err
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
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("received status %d from metadata service", resp.StatusCode)
	}

	// Convert the resp.Body to a string
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("failed to read metadata response: %v", err)
	}

	// Convert the resp.Body to a string
	bodyString := string(body)

	// Decode JSON
	var data interface{}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber() // Preserve numbers as json.Number
	if err := decoder.Decode(&data); err != nil {
		return nil, "", fmt.Errorf("failed to decode metadata response: %v", err)
	}

	return data, bodyString, nil
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
				Path:          currentPath,
				PathCorrected: convertToCabobCase(currentPath),
				Depth:         depth,
				IsTerminal:    isTerminal,
				Value:         valueStr,
			})
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

// Add a new helper function to convert camelCase to cabob case
func convertToCabobCase(s string) string {
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
