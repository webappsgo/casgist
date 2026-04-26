package v1

import (
	"net/http"

	"github.com/casapps/casgist/src/internal/docs"
	"github.com/labstack/echo/v4"
)

// DocsHandler handles API documentation endpoints
type DocsHandler struct {
	swaggerService *docs.SwaggerService
}

// NewDocsHandler creates a new documentation handler
func NewDocsHandler() *DocsHandler {
	return &DocsHandler{
		swaggerService: docs.NewSwaggerService(),
	}
}

// RegisterRoutes registers documentation routes
func (h *DocsHandler) RegisterRoutes(g *echo.Group) {
	// Main documentation endpoints
	g.GET("/docs", h.ServeSwaggerUI)
	g.GET("/docs/", h.ServeSwaggerUI) // Handle trailing slash
	g.GET("/docs/swagger", h.ServeSwaggerUI)
	g.GET("/docs/redoc", h.ServeReDoc)
	g.GET("/docs/openapi.json", h.ServeOpenAPISpec)
	g.GET("/docs/openapi.yaml", h.ServeOpenAPIYAML)
	
	// API statistics and information
	g.GET("/docs/stats", h.GetAPIStats)
	g.GET("/docs/health", h.GetDocsHealth)
	
	// Interactive API explorer
	g.GET("/docs/explorer", h.ServeAPIExplorer)
}

// ServeSwaggerUI serves the Swagger UI documentation interface
func (h *DocsHandler) ServeSwaggerUI(c echo.Context) error {
	return h.swaggerService.ServeSwaggerUI(c)
}

// ServeReDoc serves the ReDoc documentation interface
func (h *DocsHandler) ServeReDoc(c echo.Context) error {
	return h.swaggerService.ServeReDoc(c)
}

// ServeOpenAPISpec serves the OpenAPI JSON specification
func (h *DocsHandler) ServeOpenAPISpec(c echo.Context) error {
	return h.swaggerService.ServeOpenAPISpec(c)
}

// ServeOpenAPIYAML serves the OpenAPI specification in YAML format
func (h *DocsHandler) ServeOpenAPIYAML(c echo.Context) error {
	// For now, redirect to JSON - could implement YAML conversion later
	return c.Redirect(http.StatusMovedPermanently, "/api/docs/openapi.json")
}

// GetAPIStats returns statistics about the API endpoints
func (h *DocsHandler) GetAPIStats(c echo.Context) error {
	stats := h.swaggerService.GetAPIStats()
	return c.JSON(http.StatusOK, map[string]interface{}{
		"stats": stats,
	})
}

// GetDocsHealth returns health information for the documentation service
func (h *DocsHandler) GetDocsHealth(c echo.Context) error {
	return c.JSON(http.StatusOK, map[string]interface{}{
		"status": "healthy",
		"service": "documentation",
		"features": []string{
			"swagger-ui",
			"redoc",
			"openapi-spec",
			"interactive-explorer",
		},
		"endpoints": []string{
			"/api/docs",
			"/api/docs/redoc",
			"/api/docs/openapi.json",
			"/api/docs/stats",
		},
	})
}

// ServeAPIExplorer serves an interactive API explorer interface
func (h *DocsHandler) ServeAPIExplorer(c echo.Context) error {
	html := `<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>CasGists API Explorer</title>
    <link href="https://cdn.jsdelivr.net/npm/tailwindcss@2.2.19/dist/tailwind.min.css" rel="stylesheet">
    <link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/font-awesome/6.0.0/css/all.min.css">
    <style>
        body { background: #1e1e2e; color: #cdd6f4; font-family: 'Inter', sans-serif; }
        .card { background: #313244; border: 1px solid #45475a; }
        .btn-primary { background: #a6e3a1; color: #1e1e2e; }
        .btn-primary:hover { background: #94e2d5; }
        .text-primary { color: #a6e3a1; }
        .text-secondary { color: #fab387; }
        .border-primary { border-color: #a6e3a1; }
        
        pre { 
            background: #45475a; 
            border: 1px solid #585b70; 
            border-radius: 0.5rem; 
            padding: 1rem; 
            overflow-x: auto;
            font-size: 0.875rem;
            line-height: 1.5;
        }
        
        .method-get { color: #a6e3a1; background: rgba(166, 227, 161, 0.1); }
        .method-post { color: #74c0fc; background: rgba(116, 192, 252, 0.1); }
        .method-put { color: #fab387; background: rgba(250, 179, 135, 0.1); }
        .method-delete { color: #f38ba8; background: rgba(243, 139, 168, 0.1); }
        
        .endpoint-card {
            transition: all 0.2s;
            cursor: pointer;
        }
        
        .endpoint-card:hover {
            transform: translateY(-2px);
            box-shadow: 0 4px 12px rgba(0, 0, 0, 0.3);
        }
        
        .response-tabs {
            display: flex;
            border-bottom: 1px solid #45475a;
            margin-bottom: 1rem;
        }
        
        .response-tab {
            padding: 0.5rem 1rem;
            cursor: pointer;
            border-bottom: 2px solid transparent;
            transition: all 0.2s;
        }
        
        .response-tab.active {
            border-bottom-color: #a6e3a1;
            color: #a6e3a1;
        }
        
        .response-tab:hover {
            background: rgba(166, 227, 161, 0.1);
        }
    </style>
</head>
<body>
    <div class="min-h-screen">
        <!-- Header -->
        <header class="bg-gradient-to-r from-gray-900 to-gray-800 border-b-2 border-green-400 p-6">
            <div class="container mx-auto">
                <div class="flex items-center justify-between">
                    <div>
                        <h1 class="text-3xl font-bold text-primary">
                            <i class="fas fa-rocket mr-3"></i>CasGists API Explorer
                        </h1>
                        <p class="text-gray-300 mt-2">Interactive API testing and exploration tool</p>
                    </div>
                    <div class="flex space-x-4">
                        <a href="/api/docs" class="btn-primary px-4 py-2 rounded-lg text-sm font-semibold hover:bg-green-300 transition-colors">
                            <i class="fas fa-book mr-2"></i>Documentation
                        </a>
                        <a href="/" class="text-blue-400 hover:text-blue-300 px-4 py-2 rounded-lg border border-blue-400 hover:border-blue-300 text-sm font-semibold transition-colors">
                            <i class="fas fa-home mr-2"></i>Home
                        </a>
                    </div>
                </div>
            </div>
        </header>

        <div class="container mx-auto p-6">
            <div class="grid grid-cols-1 lg:grid-cols-3 gap-6">
                <!-- API Endpoints List -->
                <div class="lg:col-span-1">
                    <div class="card rounded-xl p-6">
                        <h2 class="text-xl font-bold text-primary mb-4">
                            <i class="fas fa-list mr-2"></i>API Endpoints
                        </h2>
                        
                        <!-- Filter -->
                        <div class="mb-4">
                            <input type="text" id="endpoint-filter" placeholder="Filter endpoints..." 
                                   class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded-lg text-white placeholder-gray-400 focus:border-green-400 focus:outline-none">
                        </div>
                        
                        <div id="endpoints-list" class="space-y-2">
                            <!-- Endpoints will be loaded here -->
                        </div>
                    </div>
                </div>

                <!-- API Testing Panel -->
                <div class="lg:col-span-2">
                    <div class="card rounded-xl p-6">
                        <div id="welcome-panel">
                            <div class="text-center py-12">
                                <i class="fas fa-mouse-pointer text-6xl text-gray-500 mb-6"></i>
                                <h3 class="text-2xl font-bold text-gray-300 mb-4">Select an Endpoint</h3>
                                <p class="text-gray-400">Choose an API endpoint from the list to start testing</p>
                            </div>
                        </div>
                        
                        <div id="endpoint-panel" style="display: none;">
                            <!-- Endpoint details will be loaded here -->
                        </div>
                    </div>
                </div>
            </div>
        </div>
    </div>

    <script>
        // API Explorer functionality
        let apiSpec = null;
        let currentEndpoint = null;

        // Load API specification
        async function loadAPISpec() {
            try {
                const response = await fetch('/api/docs/openapi.json');
                apiSpec = await response.json();
                renderEndpointsList();
            } catch (error) {
                console.error('Failed to load API spec:', error);
                document.getElementById('endpoints-list').innerHTML = 
                    '<div class="text-red-400 p-4">Failed to load API endpoints</div>';
            }
        }

        // Render endpoints list
        function renderEndpointsList() {
            const container = document.getElementById('endpoints-list');
            const paths = apiSpec.paths;
            
            let html = '';
            Object.keys(paths).forEach(path => {
                const pathData = paths[path];
                Object.keys(pathData).forEach(method => {
                    if (method === 'parameters') return; // Skip parameters
                    
                    const operation = pathData[method];
                    const methodClass = 'method-' + method.toLowerCase();
                    
                    html += ` + "`" + `
                        <div class="endpoint-card p-3 rounded-lg border border-gray-600 hover:border-green-400" 
                             onclick="selectEndpoint('${path}', '${method}')">
                            <div class="flex items-center justify-between">
                                <div class="flex items-center space-x-3">
                                    <span class="${methodClass} px-2 py-1 rounded text-xs font-bold uppercase">${method}</span>
                                    <span class="text-sm font-mono text-gray-300">${path}</span>
                                </div>
                            </div>
                            <div class="mt-2 text-sm text-gray-400">${operation.summary || 'No description'}</div>
                        </div>
                    ` + "`" + `;
                });
            });
            
            container.innerHTML = html;
        }

        // Select and display endpoint details
        function selectEndpoint(path, method) {
            currentEndpoint = { path, method };
            const operation = apiSpec.paths[path][method];
            
            document.getElementById('welcome-panel').style.display = 'none';
            document.getElementById('endpoint-panel').style.display = 'block';
            
            renderEndpointPanel(path, method, operation);
        }

        // Render endpoint testing panel
        function renderEndpointPanel(path, method, operation) {
            const methodClass = 'method-' + method.toLowerCase();
            
            let parametersHtml = '';
            if (operation.parameters && operation.parameters.length > 0) {
                parametersHtml = ` + "`" + `
                    <div class="mb-6">
                        <h4 class="text-lg font-semibold text-primary mb-3">Parameters</h4>
                        <div class="space-y-3">
                            ${operation.parameters.map(param => ` + "`" + `
                                <div class="flex items-center space-x-4">
                                    <div class="w-24 text-sm font-mono text-secondary">${param.in}</div>
                                    <div class="flex-1">
                                        <label class="block text-sm font-medium text-gray-300 mb-1">${param.name}</label>
                                        <input type="text" id="param-${param.name}" placeholder="${param.description || param.name}"
                                               class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-white placeholder-gray-400 focus:border-green-400 focus:outline-none">
                                    </div>
                                    <div class="text-xs ${param.required ? 'text-red-400' : 'text-gray-500'}">
                                        ${param.required ? 'Required' : 'Optional'}
                                    </div>
                                </div>
                            ` + "`" + `).join('')}
                        </div>
                    </div>
                ` + "`" + `;
            }
            
            let requestBodyHtml = '';
            if (operation.requestBody) {
                requestBodyHtml = ` + "`" + `
                    <div class="mb-6">
                        <h4 class="text-lg font-semibold text-primary mb-3">Request Body</h4>
                        <textarea id="request-body" rows="8" placeholder="Enter JSON request body..."
                                  class="w-full px-3 py-2 bg-gray-700 border border-gray-600 rounded text-white placeholder-gray-400 focus:border-green-400 focus:outline-none font-mono text-sm"></textarea>
                    </div>
                ` + "`" + `;
            }
            
            const html = ` + "`" + `
                <div>
                    <div class="flex items-center justify-between mb-6">
                        <div class="flex items-center space-x-4">
                            <span class="${methodClass} px-3 py-1 rounded text-sm font-bold uppercase">${method}</span>
                            <span class="text-lg font-mono text-gray-300">${path}</span>
                        </div>
                        <button onclick="testEndpoint()" class="btn-primary px-4 py-2 rounded-lg font-semibold">
                            <i class="fas fa-play mr-2"></i>Send Request
                        </button>
                    </div>
                    
                    <div class="mb-6">
                        <h3 class="text-xl font-bold text-gray-200 mb-2">${operation.summary}</h3>
                        <p class="text-gray-400">${operation.description || 'No description available'}</p>
                    </div>
                    
                    ${parametersHtml}
                    ${requestBodyHtml}
                    
                    <div id="response-section" class="mt-8" style="display: none;">
                        <h4 class="text-lg font-semibold text-primary mb-3">Response</h4>
                        <div class="response-tabs">
                            <div class="response-tab active" onclick="switchResponseTab('body')">Response Body</div>
                            <div class="response-tab" onclick="switchResponseTab('headers')">Headers</div>
                            <div class="response-tab" onclick="switchResponseTab('raw')">Raw</div>
                        </div>
                        <div id="response-content">
                            <!-- Response content will be displayed here -->
                        </div>
                    </div>
                </div>
            ` + "`" + `;
            
            document.getElementById('endpoint-panel').innerHTML = html;
        }

        // Test the selected endpoint
        async function testEndpoint() {
            if (!currentEndpoint) return;
            
            const { path, method } = currentEndpoint;
            const operation = apiSpec.paths[path][method];
            
            // Build URL with path parameters
            let url = path;
            if (operation.parameters) {
                operation.parameters.forEach(param => {
                    if (param.in === 'path') {
                        const value = document.getElementById('param-' + param.name)?.value;
                        if (value) {
                            url = url.replace('{' + param.name + '}', encodeURIComponent(value));
                        }
                    }
                });
                
                // Add query parameters
                const queryParams = new URLSearchParams();
                operation.parameters.forEach(param => {
                    if (param.in === 'query') {
                        const value = document.getElementById('param-' + param.name)?.value;
                        if (value) {
                            queryParams.append(param.name, value);
                        }
                    }
                });
                
                if (queryParams.toString()) {
                    url += '?' + queryParams.toString();
                }
            }
            
            // Build request options
            const options = {
                method: method.toUpperCase(),
                headers: {
                    'Content-Type': 'application/json',
                }
            };
            
            // Add CSRF token if available
            if (window.CasGists && window.CasGists.csrf) {
                options.headers['X-CSRF-Token'] = window.CasGists.csrf;
            }
            
            // Add request body if applicable
            if (operation.requestBody) {
                const bodyText = document.getElementById('request-body')?.value;
                if (bodyText) {
                    try {
                        JSON.parse(bodyText); // Validate JSON
                        options.body = bodyText;
                    } catch (error) {
                        alert('Invalid JSON in request body');
                        return;
                    }
                }
            }
            
            // Add header parameters
            if (operation.parameters) {
                operation.parameters.forEach(param => {
                    if (param.in === 'header') {
                        const value = document.getElementById('param-' + param.name)?.value;
                        if (value) {
                            options.headers[param.name] = value;
                        }
                    }
                });
            }
            
            try {
                console.log('Making request:', url, options);
                const response = await fetch(url, options);
                const responseData = {
                    status: response.status,
                    statusText: response.statusText,
                    headers: Object.fromEntries(response.headers.entries()),
                    body: null,
                    raw: null
                };
                
                const contentType = response.headers.get('content-type');
                if (contentType && contentType.includes('application/json')) {
                    responseData.body = await response.json();
                } else {
                    responseData.body = await response.text();
                }
                
                responseData.raw = JSON.stringify(responseData.body, null, 2);
                
                displayResponse(responseData);
            } catch (error) {
                console.error('Request failed:', error);
                displayResponse({
                    status: 0,
                    statusText: 'Network Error',
                    headers: {},
                    body: { error: error.message },
                    raw: error.message
                });
            }
        }

        // Display API response
        function displayResponse(responseData) {
            document.getElementById('response-section').style.display = 'block';
            
            const statusClass = responseData.status >= 200 && responseData.status < 300 ? 'text-green-400' : 'text-red-400';
            
            const content = ` + "`" + `
                <div class="mb-4">
                    <div class="flex items-center space-x-4 mb-2">
                        <span class="${statusClass} font-bold">Status: ${responseData.status} ${responseData.statusText}</span>
                    </div>
                </div>
                <div id="response-body-tab" class="response-tab-content">
                    <pre>${JSON.stringify(responseData.body, null, 2)}</pre>
                </div>
                <div id="response-headers-tab" class="response-tab-content" style="display: none;">
                    <pre>${JSON.stringify(responseData.headers, null, 2)}</pre>
                </div>
                <div id="response-raw-tab" class="response-tab-content" style="display: none;">
                    <pre>${responseData.raw}</pre>
                </div>
            ` + "`" + `;
            
            document.getElementById('response-content').innerHTML = content;
            
            // Scroll to response
            document.getElementById('response-section').scrollIntoView({ behavior: 'smooth' });
        }

        // Switch response tabs
        function switchResponseTab(tab) {
            // Update tab styles
            document.querySelectorAll('.response-tab').forEach(el => el.classList.remove('active'));
            event.target.classList.add('active');
            
            // Show/hide content
            document.querySelectorAll('.response-tab-content').forEach(el => el.style.display = 'none');
            document.getElementById('response-' + tab + '-tab').style.display = 'block';
        }

        // Filter endpoints
        document.getElementById('endpoint-filter').addEventListener('input', function(e) {
            const filter = e.target.value.toLowerCase();
            const endpoints = document.querySelectorAll('.endpoint-card');
            
            endpoints.forEach(card => {
                const text = card.textContent.toLowerCase();
                if (text.includes(filter)) {
                    card.style.display = 'block';
                } else {
                    card.style.display = 'none';
                }
            });
        });

        // Initialize the API explorer
        document.addEventListener('DOMContentLoaded', function() {
            loadAPISpec();
        });
    </script>
</body>
</html>`

	c.Response().Header().Set("Content-Type", "text/html")
	return c.HTML(http.StatusOK, html)
}