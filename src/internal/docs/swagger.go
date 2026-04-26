package docs

import (
	"embed"
	"html/template"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
)

//go:embed swagger-ui/*
var swaggerUI embed.FS

//go:embed templates/*
var templates embed.FS

// SwaggerService handles API documentation generation and serving
type SwaggerService struct {
	spec     *OpenAPISpec
	template *template.Template
}

// OpenAPISpec represents the OpenAPI 3.0 specification
type OpenAPISpec struct {
	OpenAPI    string                 `json:"openapi"`
	Info       OpenAPIInfo            `json:"info"`
	Servers    []OpenAPIServer        `json:"servers"`
	Paths      map[string]OpenAPIPath `json:"paths"`
	Components OpenAPIComponents      `json:"components"`
	Tags       []OpenAPITag           `json:"tags"`
}

type OpenAPIInfo struct {
	Title          string         `json:"title"`
	Description    string         `json:"description"`
	Version        string         `json:"version"`
	Contact        OpenAPIContact `json:"contact"`
	License        OpenAPILicense `json:"license"`
	TermsOfService string         `json:"termsOfService,omitempty"`
}

type OpenAPIContact struct {
	Name  string `json:"name,omitempty"`
	URL   string `json:"url,omitempty"`
	Email string `json:"email,omitempty"`
}

type OpenAPILicense struct {
	Name string `json:"name"`
	URL  string `json:"url,omitempty"`
}

type OpenAPIServer struct {
	URL         string                    `json:"url"`
	Description string                    `json:"description"`
	Variables   map[string]OpenAPIVariable `json:"variables,omitempty"`
}

type OpenAPIVariable struct {
	Default     string   `json:"default"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

type OpenAPIPath struct {
	Summary     string                    `json:"summary,omitempty"`
	Description string                    `json:"description,omitempty"`
	Get         *OpenAPIOperation         `json:"get,omitempty"`
	Post        *OpenAPIOperation         `json:"post,omitempty"`
	Put         *OpenAPIOperation         `json:"put,omitempty"`
	Delete      *OpenAPIOperation         `json:"delete,omitempty"`
	Patch       *OpenAPIOperation         `json:"patch,omitempty"`
	Parameters  []OpenAPIParameter        `json:"parameters,omitempty"`
}

type OpenAPIOperation struct {
	Tags        []string                     `json:"tags,omitempty"`
	Summary     string                       `json:"summary"`
	Description string                       `json:"description,omitempty"`
	OperationID string                       `json:"operationId"`
	Parameters  []OpenAPIParameter           `json:"parameters,omitempty"`
	RequestBody *OpenAPIRequestBody          `json:"requestBody,omitempty"`
	Responses   map[string]OpenAPIResponse   `json:"responses"`
	Security    []map[string][]string        `json:"security,omitempty"`
	Deprecated  bool                         `json:"deprecated,omitempty"`
}

type OpenAPIParameter struct {
	Name        string               `json:"name"`
	In          string               `json:"in"` // path, query, header, cookie
	Description string               `json:"description,omitempty"`
	Required    bool                 `json:"required,omitempty"`
	Schema      *OpenAPISchema       `json:"schema,omitempty"`
	Example     interface{}          `json:"example,omitempty"`
}

type OpenAPIRequestBody struct {
	Description string                           `json:"description,omitempty"`
	Content     map[string]OpenAPIMediaType      `json:"content"`
	Required    bool                             `json:"required,omitempty"`
}

type OpenAPIMediaType struct {
	Schema   *OpenAPISchema `json:"schema,omitempty"`
	Example  interface{}    `json:"example,omitempty"`
	Examples map[string]OpenAPIExample `json:"examples,omitempty"`
}

type OpenAPIExample struct {
	Summary     string      `json:"summary,omitempty"`
	Description string      `json:"description,omitempty"`
	Value       interface{} `json:"value,omitempty"`
}

type OpenAPIResponse struct {
	Description string                      `json:"description"`
	Headers     map[string]OpenAPIParameter `json:"headers,omitempty"`
	Content     map[string]OpenAPIMediaType `json:"content,omitempty"`
}

type OpenAPISchema struct {
	Type        string                    `json:"type,omitempty"`
	Format      string                    `json:"format,omitempty"`
	Description string                    `json:"description,omitempty"`
	Properties  map[string]*OpenAPISchema `json:"properties,omitempty"`
	Items       *OpenAPISchema            `json:"items,omitempty"`
	Required    []string                  `json:"required,omitempty"`
	Example     interface{}               `json:"example,omitempty"`
	Enum        []interface{}             `json:"enum,omitempty"`
	Default     interface{}               `json:"default,omitempty"`
	Ref         string                    `json:"$ref,omitempty"`
}

type OpenAPIComponents struct {
	Schemas         map[string]*OpenAPISchema       `json:"schemas,omitempty"`
	SecuritySchemes map[string]OpenAPISecurityScheme `json:"securitySchemes,omitempty"`
}

type OpenAPISecurityScheme struct {
	Type         string `json:"type"`
	Description  string `json:"description,omitempty"`
	Name         string `json:"name,omitempty"`
	In           string `json:"in,omitempty"`
	Scheme       string `json:"scheme,omitempty"`
	BearerFormat string `json:"bearerFormat,omitempty"`
}

type OpenAPITag struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// NewSwaggerService creates a new swagger documentation service
func NewSwaggerService() *SwaggerService {
	service := &SwaggerService{}
	service.generateSpec()
	service.loadTemplate()
	return service
}

// generateSpec creates the OpenAPI specification for CasGists
func (s *SwaggerService) generateSpec() {
	s.spec = &OpenAPISpec{
		OpenAPI: "3.0.3",
		Info: OpenAPIInfo{
			Title:       "CasGists API",
			Description: "A powerful, self-hosted alternative to GitHub Gists with advanced features",
			Version:     "1.0.0",
			Contact: OpenAPIContact{
				Name:  "CasGists Team",
				URL:   "https://github.com/casapps/casgist",
				Email: "support@casapps.com",
			},
			License: OpenAPILicense{
				Name: "MIT",
				URL:  "https://opensource.org/licenses/MIT",
			},
		},
		Servers: []OpenAPIServer{
			{
				URL:         "{protocol}://{host}:{port}",
				Description: "CasGists Server",
				Variables: map[string]OpenAPIVariable{
					"protocol": {
						Default:     "http",
						Description: "Protocol (http or https)",
						Enum:        []string{"http", "https"},
					},
					"host": {
						Default:     "localhost",
						Description: "Server hostname",
					},
					"port": {
						Default:     "8080",
						Description: "Server port",
					},
				},
			},
		},
		Paths:      make(map[string]OpenAPIPath),
		Components: s.generateComponents(),
		Tags:       s.generateTags(),
	}
	
	// Generate paths for all API endpoints
	s.generateAPIPaths()
}

func (s *SwaggerService) generateComponents() OpenAPIComponents {
	return OpenAPIComponents{
		Schemas: map[string]*OpenAPISchema{
			"User": {
				Type:        "object",
				Description: "User account information",
				Properties: map[string]*OpenAPISchema{
					"id": {
						Type:        "integer",
						Description: "Unique user identifier",
						Example:     1,
					},
					"username": {
						Type:        "string",
						Description: "Username",
						Example:     "johndoe",
					},
					"email": {
						Type:        "string",
						Format:      "email",
						Description: "User email address",
						Example:     "john@example.com",
					},
					"created_at": {
						Type:        "string",
						Format:      "date-time",
						Description: "Account creation timestamp",
					},
					"updated_at": {
						Type:        "string",
						Format:      "date-time",
						Description: "Last update timestamp",
					},
				},
				Required: []string{"id", "username", "email"},
			},
			"Gist": {
				Type:        "object",
				Description: "Code gist with files and metadata",
				Properties: map[string]*OpenAPISchema{
					"id": {
						Type:        "string",
						Description: "Unique gist identifier",
						Example:     "abc123def456",
					},
					"title": {
						Type:        "string",
						Description: "Gist title",
						Example:     "My awesome code snippet",
					},
					"description": {
						Type:        "string",
						Description: "Gist description",
						Example:     "A useful utility function",
					},
					"visibility": {
						Type:        "string",
						Description: "Gist visibility",
						Enum:        []interface{}{"public", "unlisted", "private"},
						Example:     "public",
					},
					"files": {
						Type:        "array",
						Description: "Files in this gist",
						Items: &OpenAPISchema{
							Ref: "#/components/schemas/GistFile",
						},
					},
					"user": {
						Ref: "#/components/schemas/User",
					},
					"created_at": {
						Type:        "string",
						Format:      "date-time",
						Description: "Creation timestamp",
					},
					"updated_at": {
						Type:        "string",
						Format:      "date-time",
						Description: "Last update timestamp",
					},
				},
				Required: []string{"id", "title", "visibility", "files"},
			},
			"GistFile": {
				Type:        "object",
				Description: "Individual file within a gist",
				Properties: map[string]*OpenAPISchema{
					"id": {
						Type:        "integer",
						Description: "File identifier",
						Example:     1,
					},
					"filename": {
						Type:        "string",
						Description: "File name with extension",
						Example:     "example.js",
					},
					"content": {
						Type:        "string",
						Description: "File content",
						Example:     "console.log('Hello, world!');",
					},
					"size": {
						Type:        "integer",
						Description: "File size in bytes",
						Example:     29,
					},
					"language": {
						Type:        "string",
						Description: "Programming language",
						Example:     "javascript",
					},
				},
				Required: []string{"filename", "content"},
			},
			"Error": {
				Type:        "object",
				Description: "Error response",
				Properties: map[string]*OpenAPISchema{
					"error": {
						Type:        "string",
						Description: "Error message",
						Example:     "Resource not found",
					},
					"code": {
						Type:        "integer",
						Description: "Error code",
						Example:     404,
					},
					"details": {
						Type:        "string",
						Description: "Additional error details",
						Example:     "The requested gist does not exist",
					},
				},
				Required: []string{"error"},
			},
			"CreateGistRequest": {
				Type:        "object",
				Description: "Request body for creating a new gist",
				Properties: map[string]*OpenAPISchema{
					"title": {
						Type:        "string",
						Description: "Gist title",
						Example:     "My new gist",
					},
					"description": {
						Type:        "string",
						Description: "Gist description",
						Example:     "A helpful code snippet",
					},
					"visibility": {
						Type:        "string",
						Description: "Gist visibility",
						Enum:        []interface{}{"public", "unlisted", "private"},
						Default:     "public",
					},
					"files": {
						Type:        "array",
						Description: "Files to include in the gist",
						Items: &OpenAPISchema{
							Type: "object",
							Properties: map[string]*OpenAPISchema{
								"filename": {
									Type:        "string",
									Description: "File name",
									Example:     "hello.py",
								},
								"content": {
									Type:        "string",
									Description: "File content",
									Example:     "print('Hello, world!')",
								},
							},
							Required: []string{"filename", "content"},
						},
					},
				},
				Required: []string{"title", "files"},
			},
		},
		SecuritySchemes: map[string]OpenAPISecurityScheme{
			"cookieAuth": {
				Type:        "apiKey",
				Description: "Session cookie authentication",
				Name:        "session",
				In:          "cookie",
			},
			"csrfToken": {
				Type:        "apiKey",
				Description: "CSRF protection token",
				Name:        "X-CSRF-Token",
				In:          "header",
			},
		},
	}
}

func (s *SwaggerService) generateTags() []OpenAPITag {
	return []OpenAPITag{
		{
			Name:        "Authentication",
			Description: "User authentication and session management",
		},
		{
			Name:        "Gists",
			Description: "Gist management operations",
		},
		{
			Name:        "Users",
			Description: "User account operations",
		},
		{
			Name:        "Search",
			Description: "Search functionality",
		},
		{
			Name:        "Admin",
			Description: "Administrative operations",
		},
		{
			Name:        "Health",
			Description: "System health and status",
		},
	}
}

func (s *SwaggerService) generateAPIPaths() {
	// Health endpoints
	s.spec.Paths["/health"] = OpenAPIPath{
		Get: &OpenAPIOperation{
			Tags:        []string{"Health"},
			Summary:     "Health check",
			Description: "Check the health status of the CasGists server",
			OperationID: "healthCheck",
			Responses: map[string]OpenAPIResponse{
				"200": {
					Description: "Server is healthy",
					Content: map[string]OpenAPIMediaType{
						"application/json": {
							Schema: &OpenAPISchema{
								Type: "object",
								Properties: map[string]*OpenAPISchema{
									"status": {
										Type:    "string",
										Example: "ok",
									},
									"timestamp": {
										Type:   "string",
										Format: "date-time",
									},
									"version": {
										Type:    "string",
										Example: "1.0.0",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// Authentication endpoints
	s.spec.Paths["/api/v1/auth/login"] = OpenAPIPath{
		Post: &OpenAPIOperation{
			Tags:        []string{"Authentication"},
			Summary:     "Login user",
			Description: "Authenticate user with username/email and password",
			OperationID: "loginUser",
			RequestBody: &OpenAPIRequestBody{
				Description: "Login credentials",
				Required:    true,
				Content: map[string]OpenAPIMediaType{
					"application/json": {
						Schema: &OpenAPISchema{
							Type: "object",
							Properties: map[string]*OpenAPISchema{
								"username": {
									Type:        "string",
									Description: "Username or email address",
									Example:     "johndoe",
								},
								"password": {
									Type:        "string",
									Description: "User password",
									Example:     "secretpassword",
								},
							},
							Required: []string{"username", "password"},
						},
					},
				},
			},
			Responses: map[string]OpenAPIResponse{
				"200": {
					Description: "Login successful",
					Content: map[string]OpenAPIMediaType{
						"application/json": {
							Schema: &OpenAPISchema{
								Ref: "#/components/schemas/User",
							},
						},
					},
				},
				"401": {
					Description: "Invalid credentials",
					Content: map[string]OpenAPIMediaType{
						"application/json": {
							Schema: &OpenAPISchema{
								Ref: "#/components/schemas/Error",
							},
						},
					},
				},
			},
		},
	}

	s.spec.Paths["/api/v1/auth/logout"] = OpenAPIPath{
		Post: &OpenAPIOperation{
			Tags:        []string{"Authentication"},
			Summary:     "Logout user",
			Description: "End user session and clear authentication cookies",
			OperationID: "logoutUser",
			Security: []map[string][]string{
				{"cookieAuth": {}},
			},
			Responses: map[string]OpenAPIResponse{
				"200": {
					Description: "Logout successful",
					Content: map[string]OpenAPIMediaType{
						"application/json": {
							Schema: &OpenAPISchema{
								Type: "object",
								Properties: map[string]*OpenAPISchema{
									"message": {
										Type:    "string",
										Example: "Logged out successfully",
									},
								},
							},
						},
					},
				},
			},
		},
	}

	// User endpoints
	s.spec.Paths["/api/v1/user"] = OpenAPIPath{
		Get: &OpenAPIOperation{
			Tags:        []string{"Users"},
			Summary:     "Get current user",
			Description: "Get information about the currently authenticated user",
			OperationID: "getCurrentUser",
			Security: []map[string][]string{
				{"cookieAuth": {}},
			},
			Responses: map[string]OpenAPIResponse{
				"200": {
					Description: "User information",
					Content: map[string]OpenAPIMediaType{
						"application/json": {
							Schema: &OpenAPISchema{
								Ref: "#/components/schemas/User",
							},
						},
					},
				},
				"401": {
					Description: "Not authenticated",
					Content: map[string]OpenAPIMediaType{
						"application/json": {
							Schema: &OpenAPISchema{
								Ref: "#/components/schemas/Error",
							},
						},
					},
				},
			},
		},
	}

	// Gist endpoints
	s.spec.Paths["/api/v1/gists"] = OpenAPIPath{
		Get: &OpenAPIOperation{
			Tags:        []string{"Gists"},
			Summary:     "List gists",
			Description: "Get a list of gists with optional filtering",
			OperationID: "listGists",
			Parameters: []OpenAPIParameter{
				{
					Name:        "page",
					In:          "query",
					Description: "Page number for pagination",
					Schema:      &OpenAPISchema{Type: "integer", Default: 1},
				},
				{
					Name:        "per_page",
					In:          "query",
					Description: "Number of gists per page",
					Schema:      &OpenAPISchema{Type: "integer", Default: 20},
				},
				{
					Name:        "visibility",
					In:          "query",
					Description: "Filter by visibility",
					Schema: &OpenAPISchema{
						Type: "string",
						Enum: []interface{}{"public", "unlisted", "private"},
					},
				},
				{
					Name:        "sort",
					In:          "query",
					Description: "Sort order",
					Schema: &OpenAPISchema{
						Type:    "string",
						Enum:    []interface{}{"created", "updated", "title"},
						Default: "updated",
					},
				},
			},
			Responses: map[string]OpenAPIResponse{
				"200": {
					Description: "List of gists",
					Content: map[string]OpenAPIMediaType{
						"application/json": {
							Schema: &OpenAPISchema{
								Type: "object",
								Properties: map[string]*OpenAPISchema{
									"gists": {
										Type: "array",
										Items: &OpenAPISchema{
											Ref: "#/components/schemas/Gist",
										},
									},
									"pagination": {
										Type: "object",
										Properties: map[string]*OpenAPISchema{
											"page":       {Type: "integer"},
											"per_page":   {Type: "integer"},
											"total":      {Type: "integer"},
											"total_pages": {Type: "integer"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		Post: &OpenAPIOperation{
			Tags:        []string{"Gists"},
			Summary:     "Create gist",
			Description: "Create a new gist with files",
			OperationID: "createGist",
			Security: []map[string][]string{
				{"cookieAuth": {}, "csrfToken": {}},
			},
			RequestBody: &OpenAPIRequestBody{
				Description: "Gist data",
				Required:    true,
				Content: map[string]OpenAPIMediaType{
					"application/json": {
						Schema: &OpenAPISchema{
							Ref: "#/components/schemas/CreateGistRequest",
						},
					},
				},
			},
			Responses: map[string]OpenAPIResponse{
				"201": {
					Description: "Gist created successfully",
					Content: map[string]OpenAPIMediaType{
						"application/json": {
							Schema: &OpenAPISchema{
								Ref: "#/components/schemas/Gist",
							},
						},
					},
				},
				"400": {
					Description: "Invalid request data",
					Content: map[string]OpenAPIMediaType{
						"application/json": {
							Schema: &OpenAPISchema{
								Ref: "#/components/schemas/Error",
							},
						},
					},
				},
				"401": {
					Description: "Not authenticated",
					Content: map[string]OpenAPIMediaType{
						"application/json": {
							Schema: &OpenAPISchema{
								Ref: "#/components/schemas/Error",
							},
						},
					},
				},
			},
		},
	}

	s.spec.Paths["/api/v1/gists/{id}"] = OpenAPIPath{
		Parameters: []OpenAPIParameter{
			{
				Name:        "id",
				In:          "path",
				Description: "Gist ID",
				Required:    true,
				Schema:      &OpenAPISchema{Type: "string"},
				Example:     "abc123def456",
			},
		},
		Get: &OpenAPIOperation{
			Tags:        []string{"Gists"},
			Summary:     "Get gist",
			Description: "Get a specific gist by ID",
			OperationID: "getGist",
			Responses: map[string]OpenAPIResponse{
				"200": {
					Description: "Gist details",
					Content: map[string]OpenAPIMediaType{
						"application/json": {
							Schema: &OpenAPISchema{
								Ref: "#/components/schemas/Gist",
							},
						},
					},
				},
				"404": {
					Description: "Gist not found",
					Content: map[string]OpenAPIMediaType{
						"application/json": {
							Schema: &OpenAPISchema{
								Ref: "#/components/schemas/Error",
							},
						},
					},
				},
			},
		},
		Put: &OpenAPIOperation{
			Tags:        []string{"Gists"},
			Summary:     "Update gist",
			Description: "Update an existing gist",
			OperationID: "updateGist",
			Security: []map[string][]string{
				{"cookieAuth": {}, "csrfToken": {}},
			},
			RequestBody: &OpenAPIRequestBody{
				Description: "Updated gist data",
				Required:    true,
				Content: map[string]OpenAPIMediaType{
					"application/json": {
						Schema: &OpenAPISchema{
							Ref: "#/components/schemas/CreateGistRequest",
						},
					},
				},
			},
			Responses: map[string]OpenAPIResponse{
				"200": {
					Description: "Gist updated successfully",
					Content: map[string]OpenAPIMediaType{
						"application/json": {
							Schema: &OpenAPISchema{
								Ref: "#/components/schemas/Gist",
							},
						},
					},
				},
				"400": {
					Description: "Invalid request data",
					Content: map[string]OpenAPIMediaType{
						"application/json": {
							Schema: &OpenAPISchema{
								Ref: "#/components/schemas/Error",
							},
						},
					},
				},
				"403": {
					Description: "Not authorized to update this gist",
					Content: map[string]OpenAPIMediaType{
						"application/json": {
							Schema: &OpenAPISchema{
								Ref: "#/components/schemas/Error",
							},
						},
					},
				},
				"404": {
					Description: "Gist not found",
					Content: map[string]OpenAPIMediaType{
						"application/json": {
							Schema: &OpenAPISchema{
								Ref: "#/components/schemas/Error",
							},
						},
					},
				},
			},
		},
		Delete: &OpenAPIOperation{
			Tags:        []string{"Gists"},
			Summary:     "Delete gist",
			Description: "Delete a gist",
			OperationID: "deleteGist",
			Security: []map[string][]string{
				{"cookieAuth": {}, "csrfToken": {}},
			},
			Responses: map[string]OpenAPIResponse{
				"204": {
					Description: "Gist deleted successfully",
				},
				"403": {
					Description: "Not authorized to delete this gist",
					Content: map[string]OpenAPIMediaType{
						"application/json": {
							Schema: &OpenAPISchema{
								Ref: "#/components/schemas/Error",
							},
						},
					},
				},
				"404": {
					Description: "Gist not found",
					Content: map[string]OpenAPIMediaType{
						"application/json": {
							Schema: &OpenAPISchema{
								Ref: "#/components/schemas/Error",
							},
						},
					},
				},
			},
		},
	}

	// Search endpoints
	s.spec.Paths["/api/v1/search"] = OpenAPIPath{
		Get: &OpenAPIOperation{
			Tags:        []string{"Search"},
			Summary:     "Search gists",
			Description: "Search for gists by content, title, or filename",
			OperationID: "searchGists",
			Parameters: []OpenAPIParameter{
				{
					Name:        "q",
					In:          "query",
					Description: "Search query",
					Required:    true,
					Schema:      &OpenAPISchema{Type: "string"},
					Example:     "javascript function",
				},
				{
					Name:        "page",
					In:          "query",
					Description: "Page number for pagination",
					Schema:      &OpenAPISchema{Type: "integer", Default: 1},
				},
				{
					Name:        "per_page",
					In:          "query",
					Description: "Number of results per page",
					Schema:      &OpenAPISchema{Type: "integer", Default: 20},
				},
				{
					Name:        "sort",
					In:          "query",
					Description: "Sort order",
					Schema: &OpenAPISchema{
						Type:    "string",
						Enum:    []interface{}{"relevance", "created", "updated"},
						Default: "relevance",
					},
				},
			},
			Responses: map[string]OpenAPIResponse{
				"200": {
					Description: "Search results",
					Content: map[string]OpenAPIMediaType{
						"application/json": {
							Schema: &OpenAPISchema{
								Type: "object",
								Properties: map[string]*OpenAPISchema{
									"results": {
										Type: "array",
										Items: &OpenAPISchema{
											Ref: "#/components/schemas/Gist",
										},
									},
									"total_count": {
										Type: "integer",
									},
									"pagination": {
										Type: "object",
										Properties: map[string]*OpenAPISchema{
											"page":        {Type: "integer"},
											"per_page":    {Type: "integer"},
											"total_pages": {Type: "integer"},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func (s *SwaggerService) loadTemplate() {
	var err error
	s.template, err = template.ParseFS(templates, "templates/swagger.html")
	if err != nil {
		// Fallback to embedded template
		s.template = template.Must(template.New("swagger").Parse(swaggerUITemplate))
	}
}

// ServeSwaggerUI serves the Swagger UI interface
func (s *SwaggerService) ServeSwaggerUI(c echo.Context) error {
	data := map[string]interface{}{
		"Title":   "CasGists API Documentation",
		"SpecURL": "/api/docs/openapi.json",
	}

	c.Response().Header().Set("Content-Type", "text/html")
	return s.template.Execute(c.Response().Writer, data)
}

// ServeOpenAPISpec serves the OpenAPI JSON specification
func (s *SwaggerService) ServeOpenAPISpec(c echo.Context) error {
	// Update server URL based on request
	if len(s.spec.Servers) > 0 {
		scheme := "http"
		if c.Request().TLS != nil || c.Request().Header.Get("X-Forwarded-Proto") == "https" {
			scheme = "https"
		}
		s.spec.Servers[0].URL = scheme + "://" + c.Request().Host
	}

	c.Response().Header().Set("Content-Type", "application/json")
	return c.JSON(http.StatusOK, s.spec)
}

// ServeReDoc serves the ReDoc documentation interface
func (s *SwaggerService) ServeReDoc(c echo.Context) error {
	html := `<!DOCTYPE html>
<html>
<head>
    <title>CasGists API Documentation</title>
    <meta charset="utf-8"/>
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <link href="https://fonts.googleapis.com/css?family=Montserrat:300,400,700|Roboto:300,400,700" rel="stylesheet">
    <style>
        body { margin: 0; padding: 0; }
        redoc { display: block; }
    </style>
</head>
<body>
    <redoc spec-url="/api/docs/openapi.json" theme="{ colors: { primary: { main: '#a6e3a1' } } }"></redoc>
    <script src="https://cdn.jsdelivr.net/npm/redoc@2.0.0/bundles/redoc.standalone.js"></script>
</body>
</html>`
	
	c.Response().Header().Set("Content-Type", "text/html")
	return c.HTML(http.StatusOK, html)
}

// GetAPIStats returns statistics about the API
func (s *SwaggerService) GetAPIStats() map[string]interface{} {
	endpointCount := 0
	methodCount := map[string]int{
		"GET":    0,
		"POST":   0,
		"PUT":    0,
		"DELETE": 0,
		"PATCH":  0,
	}

	for _, path := range s.spec.Paths {
		if path.Get != nil {
			methodCount["GET"]++
			endpointCount++
		}
		if path.Post != nil {
			methodCount["POST"]++
			endpointCount++
		}
		if path.Put != nil {
			methodCount["PUT"]++
			endpointCount++
		}
		if path.Delete != nil {
			methodCount["DELETE"]++
			endpointCount++
		}
		if path.Patch != nil {
			methodCount["PATCH"]++
			endpointCount++
		}
	}

	return map[string]interface{}{
		"total_endpoints": endpointCount,
		"methods":         methodCount,
		"schemas":         len(s.spec.Components.Schemas),
		"tags":            len(s.spec.Tags),
		"last_updated":    time.Now().Format(time.RFC3339),
		"openapi_version": s.spec.OpenAPI,
		"api_version":     s.spec.Info.Version,
	}
}

// Embedded Swagger UI template
const swaggerUITemplate = `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <title>{{.Title}}</title>
    <link rel="stylesheet" type="text/css" href="https://unpkg.com/swagger-ui-dist@5.9.0/swagger-ui.css" />
    <style>
        html { box-sizing: border-box; overflow: -moz-scrollbars-vertical; overflow-y: scroll; }
        *, *:before, *:after { box-sizing: inherit; }
        body { margin:0; background: #fafafa; }
        .swagger-ui .topbar { display: none; }
        .swagger-ui .info .title { color: #a6e3a1; }
    </style>
</head>
<body>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@5.9.0/swagger-ui-bundle.js"></script>
    <script src="https://unpkg.com/swagger-ui-dist@5.9.0/swagger-ui-standalone-preset.js"></script>
    <script>
        window.onload = function() {
            const ui = SwaggerUIBundle({
                url: '{{.SpecURL}}',
                dom_id: '#swagger-ui',
                deepLinking: true,
                presets: [
                    SwaggerUIBundle.presets.apis,
                    SwaggerUIStandalonePreset
                ],
                plugins: [
                    SwaggerUIBundle.plugins.DownloadUrl
                ],
                layout: "StandaloneLayout",
                validatorUrl: null,
                tryItOutEnabled: true,
                supportedSubmitMethods: ['get', 'post', 'put', 'delete', 'patch'],
                onComplete: function() {
                    console.log('Swagger UI loaded');
                }
            });
        };
    </script>
</body>
</html>`