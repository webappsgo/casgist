package domains

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/casapps/casgist/src/internal/database/models"
)

// Service handles custom domain operations
type Service struct {
	db              *gorm.DB
	serverIP        string
	certManager     *CertificateManager
	dnsValidator    *DNSValidator
	domainWhitelist []string // Optional: allowed domains
}

// NewService creates a new domain service
func NewService(db *gorm.DB, serverIP string, certManager *CertificateManager) *Service {
	return &Service{
		db:           db,
		serverIP:     serverIP,
		certManager:  certManager,
		dnsValidator: NewDNSValidator(),
	}
}

// AddCustomDomain adds a custom domain for a user or organization
func (s *Service) AddCustomDomain(domain string, userID *uuid.UUID, orgID *uuid.UUID) (*models.CustomDomain, error) {
	// Validate input
	if err := s.validateDomain(domain); err != nil {
		return nil, fmt.Errorf("invalid domain: %w", err)
	}
	
	// Check if domain already exists
	var existing models.CustomDomain
	err := s.db.Where("domain = ?", domain).First(&existing).Error
	if err == nil {
		return nil, fmt.Errorf("domain already exists")
	} else if err != gorm.ErrRecordNotFound {
		return nil, fmt.Errorf("failed to check domain existence: %w", err)
	}
	
	// Generate verification token
	token, err := s.generateVerificationToken()
	if err != nil {
		return nil, fmt.Errorf("failed to generate verification token: %w", err)
	}
	
	// Create domain record
	customDomain := &models.CustomDomain{
		Domain:            domain,
		UserID:            userID,
		OrganizationID:    orgID,
		VerificationToken: token,
		Verified:          false,
		SSLEnabled:        false,
	}
	
	if err := s.db.Create(customDomain).Error; err != nil {
		return nil, fmt.Errorf("failed to create custom domain: %w", err)
	}
	
	return customDomain, nil
}

// VerifyDomain verifies domain ownership through DNS
func (s *Service) VerifyDomain(domainID uuid.UUID) error {
	// Get domain
	var domain models.CustomDomain
	if err := s.db.First(&domain, "id = ?", domainID).Error; err != nil {
		return fmt.Errorf("domain not found: %w", err)
	}
	
	if domain.Verified {
		return fmt.Errorf("domain already verified")
	}
	
	// Check DNS verification
	if err := s.dnsValidator.VerifyOwnership(domain.Domain, domain.VerificationToken); err != nil {
		return fmt.Errorf("DNS verification failed: %w", err)
	}
	
	// Mark as verified
	now := time.Now()
	domain.Verified = true
	domain.VerifiedAt = &now
	
	if err := s.db.Save(&domain).Error; err != nil {
		return fmt.Errorf("failed to update domain: %w", err)
	}
	
	// Attempt to provision SSL certificate
	go s.provisionSSLCertificate(&domain)
	
	return nil
}

// EnableSSL enables SSL for a verified domain
func (s *Service) EnableSSL(domainID uuid.UUID) error {
	// Get domain
	var domain models.CustomDomain
	if err := s.db.First(&domain, "id = ?", domainID).Error; err != nil {
		return fmt.Errorf("domain not found: %w", err)
	}
	
	if !domain.Verified {
		return fmt.Errorf("domain must be verified before enabling SSL")
	}
	
	// Provision SSL certificate
	certPath, keyPath, expiresAt, err := s.certManager.ObtainCertificate(domain.Domain)
	if err != nil {
		return fmt.Errorf("failed to obtain SSL certificate: %w", err)
	}
	
	// Update domain record
	domain.SSLEnabled = true
	domain.SSLCertPath = certPath
	domain.SSLKeyPath = keyPath
	domain.SSLExpiresAt = &expiresAt
	
	if err := s.db.Save(&domain).Error; err != nil {
		return fmt.Errorf("failed to update domain SSL config: %w", err)
	}
	
	return nil
}

// DisableSSL disables SSL for a domain
func (s *Service) DisableSSL(domainID uuid.UUID) error {
	// Get domain
	var domain models.CustomDomain
	if err := s.db.First(&domain, "id = ?", domainID).Error; err != nil {
		return fmt.Errorf("domain not found: %w", err)
	}
	
	// Revoke certificate if it exists
	if domain.SSLCertPath != "" {
		if err := s.certManager.RevokeCertificate(domain.SSLCertPath); err != nil {
			// Log error but don't fail the operation
			fmt.Printf("Warning: failed to revoke certificate for %s: %v\n", domain.Domain, err)
		}
	}
	
	// Update domain record
	domain.SSLEnabled = false
	domain.SSLCertPath = ""
	domain.SSLKeyPath = ""
	domain.SSLExpiresAt = nil
	
	if err := s.db.Save(&domain).Error; err != nil {
		return fmt.Errorf("failed to update domain SSL config: %w", err)
	}
	
	return nil
}

// RemoveDomain removes a custom domain
func (s *Service) RemoveDomain(domainID uuid.UUID) error {
	// Get domain
	var domain models.CustomDomain
	if err := s.db.First(&domain, "id = ?", domainID).Error; err != nil {
		return fmt.Errorf("domain not found: %w", err)
	}
	
	// Revoke SSL certificate if enabled
	if domain.SSLEnabled && domain.SSLCertPath != "" {
		if err := s.certManager.RevokeCertificate(domain.SSLCertPath); err != nil {
			fmt.Printf("Warning: failed to revoke certificate for %s: %v\n", domain.Domain, err)
		}
	}
	
	// Delete domain
	if err := s.db.Delete(&domain).Error; err != nil {
		return fmt.Errorf("failed to delete domain: %w", err)
	}
	
	return nil
}

// GetUserDomains gets all domains for a user
func (s *Service) GetUserDomains(userID uuid.UUID) ([]models.CustomDomain, error) {
	var domains []models.CustomDomain
	err := s.db.Where("user_id = ?", userID).Order("created_at ASC").Find(&domains).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get user domains: %w", err)
	}
	return domains, nil
}

// GetOrganizationDomains gets all domains for an organization
func (s *Service) GetOrganizationDomains(orgID uuid.UUID) ([]models.CustomDomain, error) {
	var domains []models.CustomDomain
	err := s.db.Where("organization_id = ?", orgID).Order("created_at ASC").Find(&domains).Error
	if err != nil {
		return nil, fmt.Errorf("failed to get organization domains: %w", err)
	}
	return domains, nil
}

// GetDomainByName gets a domain by its name
func (s *Service) GetDomainByName(domain string) (*models.CustomDomain, error) {
	var customDomain models.CustomDomain
	err := s.db.Where("domain = ? AND verified = ?", domain, true).
		Preload("User").
		Preload("Organization").
		First(&customDomain).Error
	if err != nil {
		return nil, fmt.Errorf("domain not found or not verified: %w", err)
	}
	return &customDomain, nil
}

// CheckSSLRenewal checks and renews SSL certificates that are expiring soon
func (s *Service) CheckSSLRenewal() error {
	// Get domains with SSL enabled that expire within 30 days
	var domains []models.CustomDomain
	expireThreshold := time.Now().AddDate(0, 0, 30) // 30 days from now
	
	err := s.db.Where("ssl_enabled = ? AND ssl_expires_at < ?", true, expireThreshold).
		Find(&domains).Error
	if err != nil {
		return fmt.Errorf("failed to get domains for renewal: %w", err)
	}
	
	// Renew each domain
	for _, domain := range domains {
		if err := s.renewSSLCertificate(&domain); err != nil {
			fmt.Printf("Failed to renew certificate for %s: %v\n", domain.Domain, err)
			continue
		}
	}
	
	return nil
}

// GetVerificationInstructions returns instructions for domain verification
func (s *Service) GetVerificationInstructions(domainID uuid.UUID) (*VerificationInstructions, error) {
	var domain models.CustomDomain
	if err := s.db.First(&domain, "id = ?", domainID).Error; err != nil {
		return nil, fmt.Errorf("domain not found: %w", err)
	}
	
	return &VerificationInstructions{
		Domain: domain.Domain,
		Methods: []VerificationMethod{
			{
				Type:        "DNS_TXT",
				Name:        fmt.Sprintf("_casgists-challenge.%s", domain.Domain),
				Value:       domain.VerificationToken,
				Description: "Add this TXT record to your DNS configuration",
			},
			{
				Type:        "DNS_CNAME",
				Name:        domain.Domain,
				Value:       s.serverIP,
				Description: "Point your domain to our server IP address",
			},
		},
		CheckURL: fmt.Sprintf("/api/v1/domains/%s/verify", domainID),
	}, nil
}

// validateDomain validates domain format and checks against whitelist
func (s *Service) validateDomain(domain string) error {
	// Basic format validation
	if len(domain) == 0 || len(domain) > 253 {
		return fmt.Errorf("invalid domain length")
	}
	
	// Check for valid characters and structure
	if !isDomainValid(domain) {
		return fmt.Errorf("invalid domain format")
	}
	
	// Check against whitelist if configured
	if len(s.domainWhitelist) > 0 {
		allowed := false
		for _, allowedDomain := range s.domainWhitelist {
			if domain == allowedDomain || strings.HasSuffix("."+domain, "."+allowedDomain) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("domain not in whitelist")
		}
	}
	
	// Check if domain resolves
	_, err := net.LookupHost(domain)
	if err != nil {
		return fmt.Errorf("domain does not resolve: %w", err)
	}
	
	return nil
}

// generateVerificationToken generates a random verification token
func (s *Service) generateVerificationToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

// provisionSSLCertificate attempts to provision an SSL certificate
func (s *Service) provisionSSLCertificate(domain *models.CustomDomain) {
	certPath, keyPath, expiresAt, err := s.certManager.ObtainCertificate(domain.Domain)
	if err != nil {
		fmt.Printf("Failed to provision SSL certificate for %s: %v\n", domain.Domain, err)
		return
	}
	
	// Update domain record
	domain.SSLEnabled = true
	domain.SSLCertPath = certPath
	domain.SSLKeyPath = keyPath
	domain.SSLExpiresAt = &expiresAt
	
	if err := s.db.Save(domain).Error; err != nil {
		fmt.Printf("Failed to update domain SSL config for %s: %v\n", domain.Domain, err)
		return
	}
	
	fmt.Printf("SSL certificate provisioned successfully for %s\n", domain.Domain)
}

// renewSSLCertificate renews an SSL certificate
func (s *Service) renewSSLCertificate(domain *models.CustomDomain) error {
	certPath, keyPath, expiresAt, err := s.certManager.RenewCertificate(domain.SSLCertPath)
	if err != nil {
		return fmt.Errorf("failed to renew certificate: %w", err)
	}
	
	// Update domain record
	domain.SSLCertPath = certPath
	domain.SSLKeyPath = keyPath
	domain.SSLExpiresAt = &expiresAt
	
	if err := s.db.Save(domain).Error; err != nil {
		return fmt.Errorf("failed to update domain: %w", err)
	}
	
	return nil
}

// isDomainValid checks if a domain name is valid
func isDomainValid(domain string) bool {
	// Basic RFC validation - simplified
	if strings.Contains(domain, "..") {
		return false
	}
	
	if strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return false
	}
	
	parts := strings.Split(domain, ".")
	if len(parts) < 2 {
		return false
	}
	
	for _, part := range parts {
		if len(part) == 0 || len(part) > 63 {
			return false
		}
		
		if strings.HasPrefix(part, "-") || strings.HasSuffix(part, "-") {
			return false
		}
	}
	
	return true
}

// VerificationInstructions contains domain verification instructions
type VerificationInstructions struct {
	Domain   string               `json:"domain"`
	Methods  []VerificationMethod `json:"methods"`
	CheckURL string               `json:"check_url"`
}

// VerificationMethod represents a domain verification method
type VerificationMethod struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Value       string `json:"value"`
	Description string `json:"description"`
}

// DomainMiddleware creates middleware to handle custom domain routing
func (s *Service) DomainMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host := r.Host
			
			// Remove port if present
			if colonIndex := strings.Index(host, ":"); colonIndex != -1 {
				host = host[:colonIndex]
			}
			
			// Check if this is a custom domain
			customDomain, err := s.GetDomainByName(host)
			if err == nil {
				// Set custom domain context
				r.Header.Set("X-Custom-Domain", customDomain.Domain)
				if customDomain.UserID != nil {
					r.Header.Set("X-Custom-Domain-User", customDomain.UserID.String())
				}
				if customDomain.OrganizationID != nil {
					r.Header.Set("X-Custom-Domain-Org", customDomain.OrganizationID.String())
				}
			}
			
			next.ServeHTTP(w, r)
		})
	}
}

// GetTLSConfig returns TLS configuration for custom domains
func (s *Service) GetTLSConfig() *tls.Config {
	return &tls.Config{
		GetCertificate: s.getCertificate,
	}
}

// getCertificate retrieves the appropriate certificate for a domain
func (s *Service) getCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	domain := hello.ServerName
	
	// Get custom domain
	customDomain, err := s.GetDomainByName(domain)
	if err != nil {
		return nil, fmt.Errorf("no certificate found for domain: %s", domain)
	}
	
	if !customDomain.SSLEnabled {
		return nil, fmt.Errorf("SSL not enabled for domain: %s", domain)
	}
	
	// Load certificate
	cert, err := tls.LoadX509KeyPair(customDomain.SSLCertPath, customDomain.SSLKeyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load certificate for %s: %w", domain, err)
	}
	
	return &cert, nil
}