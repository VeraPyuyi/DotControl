package main

func diagnosticFailureLayer(code string) string {
	switch code {
	case "tunnel_dns_failed", "tunnel_network_failed", "tunnel_start_failed", "tunnel_tls_failed", "tunnel_network_timeout", "tunnel_http_failed":
		return "network"
	case "http_401", "tunnel_auth_failed", "credential_rejected", "tunnel_credentials_rejected", "tunnel_permission_denied", "requires_local_proxy_auth":
		return "credential"
	case "connection_closed", "driver_transport_failed", "driver_reconnection_required":
		return "driver"
	case "session_ended":
		return "session"
	case "source_dot_unconfirmed":
		return "source_dot"
	default:
		return "unknown"
	}
}
