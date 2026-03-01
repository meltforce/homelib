package crossref

import (
	"strings"

	"github.com/meltforce/homelib/internal/config"
	"github.com/meltforce/homelib/internal/model"
)

// ApplyRoles enriches hosts with role/application information from config.
func ApplyRoles(hosts []model.Host, roles config.RolesConfig) []model.Host {
	for i := range hosts {
		h := &hosts[i]

		switch h.Source {
		case "proxmox":
			if h.HostType == "node" {
				applyProxmoxNodeRole(h, roles)
			} else {
				applyGuestApplication(h, roles)
			}
		case "tailscale":
			applyTailscaleRole(h, roles)
		default:
			// For other sources, try guest application lookup
			applyGuestApplication(h, roles)
		}
	}
	return hosts
}

func applyProxmoxNodeRole(h *model.Host, roles config.RolesConfig) {
	if role, ok := roles.ProxmoxNodes[strings.ToLower(h.Name)]; ok {
		h.Application = role.InfrastructureRole
		if role.WorkloadSpecialization != "" {
			h.Category = role.WorkloadSpecialization
		}
	}
}

func applyGuestApplication(h *model.Host, roles config.RolesConfig) {
	name := strings.ToLower(h.Name)

	// Priority 1: explicit guest override
	if app, ok := roles.GuestOverrides[name]; ok {
		h.Application = app
	} else if h.Application == "" {
		// Priority 2: hostname = application
		h.Application = h.Name
	}

	// Look up category
	if h.Application != "" {
		if cat, ok := roles.ApplicationCategories[strings.ToLower(h.Application)]; ok {
			h.Category = cat
		}
	}
}

func applyTailscaleRole(h *model.Host, roles config.RolesConfig) {
	name := strings.ToLower(h.Name)
	if role, ok := roles.TailscaleDevices[name]; ok {
		if role.Application != "" {
			h.Application = role.Application
		}
		if role.Role != "" {
			h.Category = role.Role
		}
	}
}
