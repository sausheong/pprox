# pprox Makefile

.PHONY: all build install clean test help

# Build variables
BINARY_NAME=pprox
FAILOVER_CMD=pprox-failover
FAILBACK_CMD=pprox-failback
VERIFY_CMD=pprox-verify-sync
HEALTH_CMD=pprox-health-check
ENCRYPT_CMD=pprox-encrypt-credentials

INSTALL_PATH=/usr/local/bin
CONFIG_PATH=/etc/pprox

# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOMOD=$(GOCMD) mod

all: build

## build: Build all binaries
build:
	@echo "Building pprox..."
	$(GOBUILD) -o $(BINARY_NAME) -v
	@echo "Building failover automation tools..."
	$(GOBUILD) -o $(FAILOVER_CMD) cmd/failover/main.go
	$(GOBUILD) -o $(FAILBACK_CMD) cmd/failback/main.go
	$(GOBUILD) -o $(VERIFY_CMD) cmd/verify-sync/main.go
	$(GOBUILD) -o $(HEALTH_CMD) cmd/health-check/main.go
	$(GOBUILD) -o $(ENCRYPT_CMD) cmd/encrypt-credentials/main.go
	@echo "✅ Build complete!"

## install: Install binaries to system
install: build
	@echo "Installing binaries to $(INSTALL_PATH)..."
	sudo cp $(BINARY_NAME) $(INSTALL_PATH)/
	sudo cp $(FAILOVER_CMD) $(INSTALL_PATH)/
	sudo cp $(FAILBACK_CMD) $(INSTALL_PATH)/
	sudo cp $(VERIFY_CMD) $(INSTALL_PATH)/
	sudo cp $(HEALTH_CMD) $(INSTALL_PATH)/
	sudo cp $(ENCRYPT_CMD) $(INSTALL_PATH)/
	sudo chmod +x $(INSTALL_PATH)/$(BINARY_NAME)
	sudo chmod +x $(INSTALL_PATH)/$(FAILOVER_CMD)
	sudo chmod +x $(INSTALL_PATH)/$(FAILBACK_CMD)
	sudo chmod +x $(INSTALL_PATH)/$(VERIFY_CMD)
	sudo chmod +x $(INSTALL_PATH)/$(HEALTH_CMD)
	sudo chmod +x $(INSTALL_PATH)/$(ENCRYPT_CMD)
	@echo "✅ Installation complete!"
	@echo ""
	@echo "Available commands:"
	@echo "  $(BINARY_NAME)              - Start pprox server"
	@echo "  $(FAILOVER_CMD)             - Failover to RDS"
	@echo "  $(FAILBACK_CMD)             - Failback to Cloud SQL"
	@echo "  $(VERIFY_CMD)               - Verify data synchronization"
	@echo "  $(HEALTH_CMD)               - Health check"
	@echo "  $(ENCRYPT_CMD)              - Encrypt credentials"

## install-service: Install systemd service
install-service:
	@echo "Installing systemd service..."
	@if [ ! -f /etc/systemd/system/pprox.service ]; then \
		echo "Creating systemd service file..."; \
		sudo bash -c 'cat > /etc/systemd/system/pprox.service << EOF\n\
[Unit]\n\
Description=pprox PostgreSQL Proxy Server\n\
After=network.target\n\
\n\
[Service]\n\
Type=simple\n\
User=$(USER)\n\
WorkingDirectory=$(HOME)/pprox\n\
EnvironmentFile=$(CONFIG_PATH)/.env\n\
ExecStart=$(INSTALL_PATH)/$(BINARY_NAME)\n\
ExecReload=/bin/kill -HUP \$$MAINPID\n\
Restart=always\n\
RestartSec=10\n\
StandardOutput=journal\n\
StandardError=journal\n\
\n\
[Install]\n\
WantedBy=multi-user.target\n\
EOF'; \
		sudo systemctl daemon-reload; \
		echo "✅ Service installed"; \
		echo "Enable with: sudo systemctl enable pprox"; \
		echo "Start with: sudo systemctl start pprox"; \
	else \
		echo "Service already exists"; \
	fi

## setup-config: Create configuration directory
setup-config:
	@echo "Setting up configuration directory..."
	sudo mkdir -p $(CONFIG_PATH)
	sudo mkdir -p $(CONFIG_PATH)/certs
	sudo chown -R $(USER):$(USER) $(CONFIG_PATH)
	chmod 700 $(CONFIG_PATH)
	chmod 700 $(CONFIG_PATH)/certs
	@if [ ! -f $(CONFIG_PATH)/.env ]; then \
		cp .env.example $(CONFIG_PATH)/.env; \
		chmod 600 $(CONFIG_PATH)/.env; \
		echo "✅ Configuration template created at $(CONFIG_PATH)/.env"; \
		echo "⚠️  Edit $(CONFIG_PATH)/.env with your settings"; \
	else \
		echo "Configuration already exists"; \
	fi

## clean: Remove build artifacts
clean:
	@echo "Cleaning..."
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
	rm -f $(FAILOVER_CMD)
	rm -f $(FAILBACK_CMD)
	rm -f $(VERIFY_CMD)
	rm -f $(HEALTH_CMD)
	rm -f $(ENCRYPT_CMD)
	@echo "✅ Clean complete"

## test: Run tests
test:
	$(GOTEST) -v ./...

## deps: Download dependencies
deps:
	$(GOMOD) download
	$(GOMOD) tidy

## uninstall: Remove installed binaries
uninstall:
	@echo "Uninstalling..."
	sudo rm -f $(INSTALL_PATH)/$(BINARY_NAME)
	sudo rm -f $(INSTALL_PATH)/$(FAILOVER_CMD)
	sudo rm -f $(INSTALL_PATH)/$(FAILBACK_CMD)
	sudo rm -f $(INSTALL_PATH)/$(VERIFY_CMD)
	sudo rm -f $(INSTALL_PATH)/$(HEALTH_CMD)
	sudo rm -f $(INSTALL_PATH)/$(ENCRYPT_CMD)
	@echo "✅ Uninstall complete"

## help: Show this help message
help:
	@echo "pprox - PostgreSQL Proxy Server"
	@echo ""
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' | sed -e 's/^/ /'

.DEFAULT_GOAL := help
