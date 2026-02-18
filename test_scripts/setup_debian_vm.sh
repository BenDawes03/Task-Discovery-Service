#!/bin/bash
# Setup script for Debian VMs - Install PowerShell and dependencies
# Run this on each Debian VM before running load tests

set -e

echo "=========================================="
echo "TDS VM Setup Script for Debian"
echo "=========================================="
echo ""

# Detect Debian version
if [ -f /etc/debian_version ]; then
    DEBIAN_VERSION=$(cat /etc/debian_version | cut -d. -f1)
    echo "Detected Debian version: $DEBIAN_VERSION"
else
    echo "ERROR: Not a Debian system"
    exit 1
fi

# Update package lists
echo ""
echo "[1/4] Updating package lists..."
sudo apt-get update

# Install prerequisites
echo ""
echo "[2/4] Installing prerequisites..."
sudo apt-get install -y wget apt-transport-https software-properties-common curl

# Install PowerShell
echo ""
echo "[3/4] Installing PowerShell..."

# Try package manager first
PWSH_INSTALLED=false

# Determine correct package URL
if [ "$DEBIAN_VERSION" = "11" ]; then
    PACKAGE_URL="https://packages.microsoft.com/config/debian/11/packages-microsoft-prod.deb"
elif [ "$DEBIAN_VERSION" = "12" ]; then
    PACKAGE_URL="https://packages.microsoft.com/config/debian/12/packages-microsoft-prod.deb"
else
    echo "Unknown Debian version, trying Debian 11 package..."
    PACKAGE_URL="https://packages.microsoft.com/config/debian/11/packages-microsoft-prod.deb"
fi

echo "Downloading Microsoft package repository config..."
wget -q "$PACKAGE_URL" -O /tmp/packages-microsoft-prod.deb

if [ $? -eq 0 ]; then
    echo "Installing Microsoft repository..."
    sudo dpkg -i /tmp/packages-microsoft-prod.deb
    sudo apt-get update
    
    echo "Installing PowerShell..."
    if sudo apt-get install -y powershell; then
        PWSH_INSTALLED=true
        echo "✓ PowerShell installed via apt"
    fi
    
    rm -f /tmp/packages-microsoft-prod.deb
fi

# Fallback to snap if apt installation failed
if [ "$PWSH_INSTALLED" = false ]; then
    echo "APT installation failed, trying snap..."
    
    # Install snapd if not present
    if ! command -v snap &> /dev/null; then
        echo "Installing snapd..."
        sudo apt-get install -y snapd
        sudo systemctl enable --now snapd.socket
        sleep 2
    fi
    
    echo "Installing PowerShell via snap..."
    sudo snap install powershell --classic
    
    if [ $? -eq 0 ]; then
        PWSH_INSTALLED=true
        echo "✓ PowerShell installed via snap"
    fi
fi

if [ "$PWSH_INSTALLED" = false ]; then
    echo "ERROR: Failed to install PowerShell"
    exit 1
fi

# Verify PowerShell installation
echo ""
echo "[4/4] Verifying installation..."
if command -v pwsh &> /dev/null; then
    PWSH_VERSION=$(pwsh --version)
    echo "✓ PowerShell is installed: $PWSH_VERSION"
else
    echo "ERROR: PowerShell command not found after installation"
    exit 1
fi

# Create test directory
echo ""
echo "Creating test directory..."
mkdir -p /tmp/tds-test
chmod 755 /tmp/tds-test

echo ""
echo "=========================================="
echo "Setup Complete!"
echo "=========================================="
echo ""
echo "PowerShell: $(pwsh --version)"
echo "Test directory: /tmp/tds-test"
echo ""
echo "Next steps:"
echo "1. Copy test scripts to /tmp/tds-test/"
echo "2. Run: pwsh /tmp/tds-test/vm_dummy_services.ps1"
echo "   or: pwsh /tmp/tds-test/vm_query_load.ps1"
echo ""
