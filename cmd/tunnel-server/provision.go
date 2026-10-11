package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

/*
Auto TLS for tunnel domains (option C).

When -auto-tls is set, the gateway provisions each attached custom domain on the
nginx box it runs on: write an nginx vhost that proxies the domain to the gateway
on 127.0.0.1:<port>, reload nginx, then run certbot to issue + install the cert.
Detaching removes the vhost. The gateway must run as root (it edits /etc/nginx
and runs certbot/systemctl) with nginx + certbot installed.

This keeps nginx as the single TLS terminator (no port conflict) while making
custom-domain HTTPS completely hands-off.
*/

var (
	autoTLS         bool   // enable auto vhost + certbot provisioning
	acmeEmail       string // Let's Encrypt contact email
	gatewayHTTPPort = "8008"
)

const (
	nginxSitesAvail   = "/etc/nginx/sites-available"
	nginxSitesEnabled = "/etc/nginx/sites-enabled"
)

func nginxVhost(domain string) string {
	return fmt.Sprintf(`# Managed by Sixter — tunnel domain (do not edit by hand)
server {
    listen 80;
    listen [::]:80;
    server_name %s;
    location / {
        proxy_pass http://127.0.0.1:%s;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_read_timeout 86400;
    }
}
`, domain, gatewayHTTPPort)
}

// provisionDomain writes the vhost, reloads nginx, then issues the cert. Safe to
// re-run: nginx config is overwritten and certbot keeps an existing valid cert.
func provisionDomain(domain string) error {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return fmt.Errorf("empty domain")
	}

	avail := filepath.Join(nginxSitesAvail, domain)
	if err := os.WriteFile(avail, []byte(nginxVhost(domain)), 0644); err != nil {
		return fmt.Errorf("write vhost: %w", err)
	}
	link := filepath.Join(nginxSitesEnabled, domain)
	_ = os.Remove(link)
	if err := os.Symlink(avail, link); err != nil {
		return fmt.Errorf("symlink: %w", err)
	}

	if out, err := exec.Command("nginx", "-t").CombinedOutput(); err != nil {
		return fmt.Errorf("nginx -t failed: %s", strings.TrimSpace(string(out)))
	}
	if out, err := exec.Command("systemctl", "reload", "nginx").CombinedOutput(); err != nil {
		return fmt.Errorf("nginx reload failed: %s", strings.TrimSpace(string(out)))
	}

	args := []string{"--nginx", "-d", domain, "--non-interactive", "--agree-tos",
		"--redirect", "--keep-until-expiring"}
	if acmeEmail != "" {
		args = append(args, "-m", acmeEmail)
	} else {
		args = append(args, "--register-unsafely-without-email")
	}
	if out, err := exec.Command("certbot", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("certbot failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// deprovisionDomain removes the vhost and reloads nginx. The cert is left in
// certbot's store (harmless; auto-expires) so re-attaching is instant.
func deprovisionDomain(domain string) error {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return nil
	}
	_ = os.Remove(filepath.Join(nginxSitesEnabled, domain))
	_ = os.Remove(filepath.Join(nginxSitesAvail, domain))
	_, _ = exec.Command("systemctl", "reload", "nginx").CombinedOutput()
	return nil
}
