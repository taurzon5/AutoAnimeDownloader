# Makefile for AutoAnimeDownloader installation
# Run 'make install-user' or 'sudo make install-global'

DAEMON_BINARY := autoanimedownloader-daemon
CLI_BINARY := autoanimedownloader
SERVICE_NAME := autoanimedownloader.service
DESKTOP_FILE := autoanimedownloader.desktop
ICON_FILE := icon.png
USER_INSTALL_DIR := $(HOME)/.local/bin
GLOBAL_INSTALL_DIR := /usr/local/bin
SERVICE_DIR := $(HOME)/.config/systemd/user
APPLICATIONS_DIR := $(HOME)/.local/share/applications
ICONS_DIR := $(HOME)/.local/share/icons
SYSTEM_SERVICE_FILE := autoanimedownloader-system.service
SYSTEM_SERVICE_DIR := /etc/systemd/system
# The account the system service runs as (install-system). Created as a system user, home
# SERVICE_HOME, when it doesn't exist; an existing one is used as it is.
SERVICE_USER ?= autoanimedownloader
SERVICE_HOME ?= /var/lib/autoanimedownloader

# When running as root via sudo, user-level ops (systemctl --user, desktop entries)
# must run as the real user. These macros detect the case and delegate accordingly.

define do-install-binaries
	@echo "Installing binaries..."
	@mkdir -p $(1)
	install -Dm00755 $(DAEMON_BINARY) $(1)/$(DAEMON_BINARY)
	install -Dm00755 $(CLI_BINARY) $(1)/$(CLI_BINARY)
	@echo "Binaries installed successfully."
endef

define do-install-service
	@echo "Installing systemd user service..."
	@if [ "$$(id -u)" = "0" ] && [ -n "$$SUDO_USER" ]; then \
		_UID=$$(id -u "$$SUDO_USER"); \
		_HOME=$$(getent passwd "$$SUDO_USER" | cut -d: -f6); \
		mkdir -p "$$_HOME/.config/systemd/user"; \
		sed 's|%h/.local/bin|$(1)|g' $(SERVICE_NAME) | sudo -u "$$SUDO_USER" tee "$$_HOME/.config/systemd/user/$(SERVICE_NAME)" > /dev/null; \
		sudo -u "$$SUDO_USER" env HOME="$$_HOME" XDG_RUNTIME_DIR=/run/user/$$_UID DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/$$_UID/bus \
			bash -c 'systemctl --user daemon-reload && systemctl --user stop $(SERVICE_NAME) 2>/dev/null; systemctl --user enable $(SERVICE_NAME) && systemctl --user start $(SERVICE_NAME)'; \
	else \
		mkdir -p "$(SERVICE_DIR)"; \
		sed 's|%h/.local/bin|$(1)|g' $(SERVICE_NAME) > "$(SERVICE_DIR)/$(SERVICE_NAME)"; \
		chmod 644 "$(SERVICE_DIR)/$(SERVICE_NAME)"; \
		systemctl --user daemon-reload; \
		systemctl --user stop $(SERVICE_NAME) 2>/dev/null || true; \
		systemctl --user enable $(SERVICE_NAME); \
		systemctl --user start $(SERVICE_NAME); \
	fi
	@echo "Service installed and started."
endef

define do-install-desktop
	@echo "Installing desktop entry..."
	@if [ "$$(id -u)" = "0" ] && [ -n "$$SUDO_USER" ]; then \
		_HOME=$$(getent passwd "$$SUDO_USER" | cut -d: -f6); \
		sudo -u "$$SUDO_USER" install -Dm00644 $(DESKTOP_FILE) "$$_HOME/.local/share/applications/$(DESKTOP_FILE)"; \
		sudo -u "$$SUDO_USER" install -Dm00644 $(ICON_FILE) "$$_HOME/.local/share/icons/autoanimedownloader.png"; \
	else \
		install -Dm00644 $(DESKTOP_FILE) "$(APPLICATIONS_DIR)/$(DESKTOP_FILE)"; \
		install -Dm00644 $(ICON_FILE) "$(ICONS_DIR)/autoanimedownloader.png"; \
	fi
	@echo "Desktop entry installed successfully."
endef

define do-uninstall-binaries
	@echo "Removing binaries..."
	-rm -f $(1)/$(DAEMON_BINARY)
	-rm -f $(1)/$(CLI_BINARY)
	@echo "Binaries removed."
endef

define do-uninstall-service
	@echo "Stopping and disabling service..."
	@if [ "$$(id -u)" = "0" ] && [ -n "$$SUDO_USER" ]; then \
		_UID=$$(id -u "$$SUDO_USER"); \
		_HOME=$$(getent passwd "$$SUDO_USER" | cut -d: -f6); \
		sudo -u "$$SUDO_USER" env HOME="$$_HOME" XDG_RUNTIME_DIR=/run/user/$$_UID DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/$$_UID/bus \
			bash -c 'systemctl --user stop $(SERVICE_NAME) 2>/dev/null; systemctl --user disable $(SERVICE_NAME) 2>/dev/null'; \
		sudo -u "$$SUDO_USER" rm -f "$$_HOME/.config/systemd/user/$(SERVICE_NAME)"; \
		sudo -u "$$SUDO_USER" env HOME="$$_HOME" XDG_RUNTIME_DIR=/run/user/$$_UID DBUS_SESSION_BUS_ADDRESS=unix:path=/run/user/$$_UID/bus \
			systemctl --user daemon-reload; \
	else \
		systemctl --user stop $(SERVICE_NAME) 2>/dev/null || true; \
		systemctl --user disable $(SERVICE_NAME) 2>/dev/null || true; \
		rm -f "$(SERVICE_DIR)/$(SERVICE_NAME)"; \
		systemctl --user daemon-reload; \
	fi
	@echo "Service removed."
endef

define do-uninstall-desktop
	@echo "Removing desktop entry..."
	@if [ "$$(id -u)" = "0" ] && [ -n "$$SUDO_USER" ]; then \
		_HOME=$$(getent passwd "$$SUDO_USER" | cut -d: -f6); \
		sudo -u "$$SUDO_USER" rm -f "$$_HOME/.local/share/applications/$(DESKTOP_FILE)"; \
		sudo -u "$$SUDO_USER" rm -f "$$_HOME/.local/share/icons/autoanimedownloader.png"; \
	else \
		rm -f "$(APPLICATIONS_DIR)/$(DESKTOP_FILE)"; \
		rm -f "$(ICONS_DIR)/autoanimedownloader.png"; \
	fi
	@echo "Desktop entry removed."
endef

.PHONY: install-user install-global install-system uninstall-user uninstall-global uninstall-system

default:
	@echo "Run 'make install-user' to install for current user (~/.local/bin)"
	@echo "Run 'sudo make install-global' to install system-wide (/usr/local/bin)"
	@echo "Run 'sudo make install-system' on a headless server or LXC container (a system service)"
	@echo "Run 'make uninstall-user', 'sudo make uninstall-global' or 'sudo make uninstall-system' to uninstall"

install-user:
	$(call do-install-binaries,$(USER_INSTALL_DIR))
	$(call do-install-service,$(USER_INSTALL_DIR))
	$(call do-install-desktop)
	@echo ""
	@echo "Installation complete!"
	@echo "Daemon installed in $(USER_INSTALL_DIR)/$(DAEMON_BINARY)"
	@echo "CLI installed in $(USER_INSTALL_DIR)/$(CLI_BINARY)"
	@echo "Systemd service installed and enabled."
	@echo "Desktop entry installed."

install-global:
	@if [ "$$(id -u)" != "0" ]; then echo "Error: sudo required. Run: sudo make install-global"; exit 1; fi
	$(call do-install-binaries,$(GLOBAL_INSTALL_DIR))
	$(call do-install-service,$(GLOBAL_INSTALL_DIR))
	$(call do-install-desktop)
	@echo ""
	@echo "Installation complete!"
	@echo "Daemon installed in $(GLOBAL_INSTALL_DIR)/$(DAEMON_BINARY)"
	@echo "CLI installed in $(GLOBAL_INSTALL_DIR)/$(CLI_BINARY)"
	@echo "Systemd service installed and enabled."
	@echo "Desktop entry installed."

uninstall-user:
	$(call do-uninstall-service)
	$(call do-uninstall-binaries,$(USER_INSTALL_DIR))
	$(call do-uninstall-desktop)
	@echo "Uninstallation complete!"

# install-system is for a machine with no desktop and no logged-in user (a headless server, an LXC
# container): a system service started at boot without lingering, and no desktop entry. Library
# folders bind-mounted from the host must be writable by SERVICE_USER's uid (with LXC, mind the
# idmap).
install-system:
	@if [ "$$(id -u)" != "0" ]; then echo "Error: root required. Run: sudo make install-system"; exit 1; fi
	@if ! id -u "$(SERVICE_USER)" >/dev/null 2>&1; then \
		echo "Creating system user $(SERVICE_USER) (home $(SERVICE_HOME))..."; \
		useradd --system --home-dir "$(SERVICE_HOME)" --create-home --shell /usr/sbin/nologin "$(SERVICE_USER)"; \
	fi
	systemctl stop $(SERVICE_NAME) 2>/dev/null || true
	$(call do-install-binaries,$(GLOBAL_INSTALL_DIR))
	@_GROUP=$$(id -gn "$(SERVICE_USER)"); \
		sed -e 's|@USER@|$(SERVICE_USER)|' -e "s|@GROUP@|$$_GROUP|" -e 's|@BINDIR@|$(GLOBAL_INSTALL_DIR)|' \
			$(SYSTEM_SERVICE_FILE) > "$(SYSTEM_SERVICE_DIR)/$(SERVICE_NAME)"
	chmod 644 "$(SYSTEM_SERVICE_DIR)/$(SERVICE_NAME)"
	systemctl daemon-reload
	systemctl enable --now $(SERVICE_NAME)
	@echo ""
	@echo "Installation complete!"
	@echo "Daemon installed in $(GLOBAL_INSTALL_DIR)/$(DAEMON_BINARY), running as $(SERVICE_USER)"
	@echo "Data folder: .autoAnimeDownloader in the home of $(SERVICE_USER)"
	@echo "Open the web UI from another machine: http://<this machine's address>:8091"

uninstall-global:
	@if [ "$$(id -u)" != "0" ]; then echo "Error: sudo required. Run: sudo make uninstall-global"; exit 1; fi
	$(call do-uninstall-service)
	$(call do-uninstall-binaries,$(GLOBAL_INSTALL_DIR))
	$(call do-uninstall-desktop)
	@echo "Uninstallation complete!"

uninstall-system:
	@if [ "$$(id -u)" != "0" ]; then echo "Error: root required. Run: sudo make uninstall-system"; exit 1; fi
	-systemctl disable --now $(SERVICE_NAME)
	-rm -f "$(SYSTEM_SERVICE_DIR)/$(SERVICE_NAME)"
	systemctl daemon-reload
	$(call do-uninstall-binaries,$(GLOBAL_INSTALL_DIR))
	@echo "Uninstallation complete! The user $(SERVICE_USER) and its data folder were kept."
