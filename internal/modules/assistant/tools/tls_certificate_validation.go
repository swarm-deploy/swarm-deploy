package tools

import (
	"bytes"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	tlsStatusNotChecked    = "not_checked"
	tlsStatusInvalid       = "invalid"
	tlsStageValidation     = "validation"
	tlsCodeExpired         = "certificate_expired"
	tlsCodeNotYetValid     = "certificate_not_yet_valid"
	tlsCodeTrustStore      = "trust_store_unavailable"
	tlsCodeServerAuth      = "server_auth_incompatible"
	tlsExpiryWarningWindow = 30 * 24 * time.Hour
	tlsMaxPresentedDetails = 16
	tlsMaxSANDetails       = 32
	tlsMaxTextBytes        = 512
	tlsDirectChainLength   = 2 // Leaf and trust anchor only.
)

type tlsCheckValidation struct {
	// Status is valid, invalid, or not_checked when validation could not reach a verdict.
	Status string `json:"status"`
	// ValidityPeriodOK reports whether the leaf is currently within its validity period.
	ValidityPeriodOK *bool `json:"validity_period_ok,omitempty"`
	// HostnameMatches reports whether the leaf covers the effective server name or IP.
	HostnameMatches *bool `json:"hostname_matches,omitempty"`
	// ServerAuthOK reports whether the path supports TLS Server Authentication, or a known leaf restriction.
	ServerAuthOK *bool `json:"server_auth_ok,omitempty"`
}

type tlsCheckChain struct {
	// Presented lists summaries from the wire in peer order, starting with the leaf.
	Presented []tlsCertificateSummary `json:"presented"`
	// PresentedCount counts every certificate the peer sent.
	PresentedCount int `json:"presented_count"`
	// Status is verified, unverified, or unavailable for system path verification independent of name and EKU.
	Status string `json:"status"`
}

type tlsCertificateSummary struct {
	// Subject is the certificate's distinguished name.
	Subject string `json:"subject"`
	// Issuer is the issuing certificate's distinguished name.
	Issuer string `json:"issuer"`
	// NotBefore is the UTC start of the validity period.
	NotBefore time.Time `json:"not_before"`
	// NotAfter is the UTC end of the validity period.
	NotAfter time.Time `json:"not_after"`
	// SHA256Fingerprint is the lowercase hex SHA-256 digest of the DER certificate.
	SHA256Fingerprint string `json:"sha256_fingerprint"`
}

type tlsLeafCertificate struct {
	tlsCertificateSummary

	// DNSSANs are the leaf certificate's DNS subject alternative names.
	DNSSANs []string `json:"dns_sans,omitempty"`
	// IPSANs are the leaf certificate's IP subject alternative names.
	IPSANs []string `json:"ip_sans,omitempty"`
	// SANsTruncated reports whether any SAN was clipped or omitted from the output.
	SANsTruncated bool `json:"sans_truncated,omitempty"`
	// RemainingSeconds is the signed whole-second interval to NotAfter at the time of checking.
	RemainingSeconds int64 `json:"remaining_seconds"`
	// SelfSigned reports a verified signature by the certificate's own key with matching issuer and subject.
	SelfSigned bool `json:"self_signed"`
}

func (r *tlsCheckResult) inspectCertificates(peer []*x509.Certificate, roots *x509.CertPool, now time.Time) {
	leaf := peer[0]
	r.Certificate = describeTLSLeaf(leaf, now)
	r.Chain = &tlsCheckChain{PresentedCount: len(peer), Status: "unverified"}
	for _, cert := range peer[:min(len(peer), tlsMaxPresentedDetails)] {
		r.Chain.Presented = append(r.Chain.Presented, summarizeTLSCertificate(cert))
	}

	periodCode := tlsCertificatePeriodCode(leaf, now)
	periodOK := periodCode == ""
	r.Validation.ValidityPeriodOK = &periodOK
	if periodOK && leaf.NotAfter.Sub(now) <= tlsExpiryWarningWindow {
		r.Warnings = append(r.Warnings, tlsCheckIssue{
			Stage: tlsStageValidation, Code: "certificate_expiring_soon",
			Message: "The server certificate expires within 30 days.",
		})
	}
	nameErr := leaf.VerifyHostname(r.Connection.ServerName)
	nameOK := nameErr == nil
	r.Validation.HostnameMatches = &nameOK
	if nameErr != nil {
		r.addTLSValidationError("hostname_mismatch", nameErr.Error())
	}

	chainCode := r.verifyTLSPath(peer, roots, now)
	if !periodOK && chainCode != periodCode {
		r.addTLSValidationError(periodCode, "The server certificate is outside its validity period.")
	}
	switch {
	case !periodOK || !nameOK, r.Validation.ServerAuthOK != nil && !*r.Validation.ServerAuthOK:
		r.Validation.Status = tlsStatusInvalid
	case chainCode == "":
		r.Validation.Status = "valid"
	case chainCode != tlsCodeTrustStore:
		r.Validation.Status = tlsStatusInvalid
	}
}

func (r *tlsCheckResult) verifyTLSPath(
	peer []*x509.Certificate, roots *x509.CertPool, now time.Time,
) string {
	leaf := peer[0]
	opts := x509.VerifyOptions{
		Roots:         roots,
		Intermediates: x509.NewCertPool(),
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}
	for _, cert := range peer[1:] {
		opts.Intermediates.AddCert(cert)
	}
	chains, err := leaf.Verify(opts)
	if err != nil {
		code := tlsPathErrorCode(err, now)
		if code == tlsCodeTrustStore {
			r.Chain.Status = "unavailable"
		}
		r.addTLSValidationError(code, err.Error())
		if !tlsLeafAllowsServerAuth(leaf) {
			no := false
			r.Validation.ServerAuthOK = &no
			r.addTLSValidationError(
				tlsCodeServerAuth, "The server certificate's extended key usage excludes TLS Server Authentication.",
			)
		}
		return code
	}

	r.Chain.Status = "verified"
	opts.KeyUsages = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	authChains, authErr := leaf.Verify(opts)
	var authCode string
	if authErr != nil {
		authCode = tlsPathErrorCode(authErr, now)
		r.addTLSValidationError(authCode, authErr.Error())
	} else {
		chains = authChains
	}
	if authErr == nil || authCode == tlsCodeServerAuth {
		authOK := authErr == nil
		r.Validation.ServerAuthOK = &authOK
	}
	if tlsMissingIntermediate(chains, peer) {
		r.Warnings = append(r.Warnings, tlsCheckIssue{
			Stage: tlsStageValidation, Code: "incomplete_chain",
			Message: "Every verified path uses an intermediate certificate the server did not send.",
		})
	}
	return authCode
}

func (r *tlsCheckResult) addTLSValidationError(code, message string) {
	r.Errors = append(r.Errors, tlsCheckIssue{Stage: tlsStageValidation, Code: code, Message: tlsBoundText(message)})
}

func tlsCertificatePeriodCode(cert *x509.Certificate, now time.Time) string {
	switch {
	case now.Before(cert.NotBefore):
		return tlsCodeNotYetValid
	case now.After(cert.NotAfter):
		return tlsCodeExpired
	default:
		return ""
	}
}

func tlsPathErrorCode(err error, now time.Time) string {
	var invalid x509.CertificateInvalidError
	var unknownAuthority x509.UnknownAuthorityError
	var systemRoots x509.SystemRootsError
	switch {
	case errors.As(err, &invalid) && invalid.Reason == x509.Expired && invalid.Cert != nil:
		if code := tlsCertificatePeriodCode(invalid.Cert, now); code != "" {
			return code
		}
	case errors.As(err, &invalid) && invalid.Reason == x509.IncompatibleUsage:
		return tlsCodeServerAuth
	case errors.As(err, &unknownAuthority):
		return "unknown_authority"
	case errors.As(err, &systemRoots):
		return tlsCodeTrustStore
	}
	return "certificate_verification_failed"
}

func tlsLeafAllowsServerAuth(leaf *x509.Certificate) bool {
	return len(leaf.ExtKeyUsage)+len(leaf.UnknownExtKeyUsage) == 0 ||
		slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageAny) ||
		slices.Contains(leaf.ExtKeyUsage, x509.ExtKeyUsageServerAuth)
}

// A trust error alone cannot distinguish a missing intermediate from an untrusted authority.
// Only compare the wire list against paths actually verified by X.509.
func tlsMissingIntermediate(chains [][]*x509.Certificate, peer []*x509.Certificate) bool {
	for _, chain := range chains {
		if len(chain) <= tlsDirectChainLength {
			return false
		}
		if !slices.ContainsFunc(chain[1:len(chain)-1], func(cert *x509.Certificate) bool {
			return !slices.ContainsFunc(peer[1:], cert.Equal)
		}) {
			return false
		}
	}
	return len(chains) > 0
}

func describeTLSLeaf(cert *x509.Certificate, now time.Time) *tlsLeafCertificate {
	leaf := &tlsLeafCertificate{
		tlsCertificateSummary: summarizeTLSCertificate(cert),
		RemainingSeconds:      int64(cert.NotAfter.Sub(now) / time.Second),
		SelfSigned: bytes.Equal(cert.RawSubject, cert.RawIssuer) &&
			cert.CheckSignature(cert.SignatureAlgorithm, cert.RawTBSCertificate, cert.Signature) == nil,
		SANsTruncated: len(cert.DNSNames) > tlsMaxSANDetails || len(cert.IPAddresses) > tlsMaxSANDetails,
	}
	for _, name := range cert.DNSNames[:min(len(cert.DNSNames), tlsMaxSANDetails)] {
		bounded := tlsBoundText(name)
		leaf.DNSSANs = append(leaf.DNSSANs, bounded)
		leaf.SANsTruncated = leaf.SANsTruncated || bounded != name
	}
	for _, ip := range cert.IPAddresses[:min(len(cert.IPAddresses), tlsMaxSANDetails)] {
		leaf.IPSANs = append(leaf.IPSANs, ip.String())
	}
	return leaf
}

func summarizeTLSCertificate(cert *x509.Certificate) tlsCertificateSummary {
	fingerprint := sha256.Sum256(cert.Raw)
	return tlsCertificateSummary{
		Subject:           tlsBoundText(cert.Subject.String()),
		Issuer:            tlsBoundText(cert.Issuer.String()),
		NotBefore:         cert.NotBefore.UTC(),
		NotAfter:          cert.NotAfter.UTC(),
		SHA256Fingerprint: hex.EncodeToString(fingerprint[:]),
	}
}

func tlsBoundText(value string) string {
	if len(value) <= tlsMaxTextBytes {
		return value
	}
	end := tlsMaxTextBytes
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return strings.ToValidUTF8(value[:end], "�") + "…"
}
