#!/bin/bash

# TigrisFS Installation Script
# Downloads and installs the latest release from GitHub

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Configuration
REPO="tigrisdata/tigrisfs"
INSTALL_DIR="${INSTALL_DIR:-/usr/bin}"
BINARY_NAME="tigrisfs"

# Function to print colored output
print_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Function to detect architecture
detect_arch() {
    local arch
    arch=$(uname -m)

    case $arch in
        x86_64|amd64)
            echo "amd64"
            ;;
        aarch64|arm64)
            echo "arm64"
            ;;
        armv7l|armv6l)
            echo "arm"
            ;;
        i386|i686)
            echo "386"
            ;;
        *)
            print_error "Unsupported architecture: $arch"
            exit 1
            ;;
    esac
}

# Function to detect package manager preference
detect_package_preference() {
    local os="$1"

    # Use forced package type if specified
    if [ -n "$FORCE_PACKAGE_TYPE" ]; then
        echo "$FORCE_PACKAGE_TYPE"
        return
    fi

    if [ "$os" != "linux" ]; then
        echo "tar.gz"
        return
    fi

    # Check for package managers in order of preference
    if command_exists dpkg && [ -z "$FORCE_TARBALL" ]; then
        echo "deb"
    elif command_exists rpm && [ -z "$FORCE_TARBALL" ]; then
        echo "rpm"
    elif command_exists apk && [ -z "$FORCE_TARBALL" ]; then
        echo "apk"
    else
        echo "tar.gz"
    fi
}

# Function to detect OS
detect_os() {
    local os
    os=$(uname -s | tr '[:upper:]' '[:lower:]')

    case $os in
        linux)
            echo "linux"
            ;;
        darwin)
            echo "darwin"
            ;;
        windows*|mingw*|msys*)
            echo "windows"
            ;;
        freebsd)
            echo "freebsd"
            ;;
        *)
            print_error "Unsupported operating system: $os"
            exit 1
            ;;
    esac
}

# Function to check if command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Function to check dependencies
check_dependencies() {
    local missing_deps=()
    local package_type="$1"

    if ! command_exists curl && ! command_exists wget; then
        missing_deps+=("curl or wget")
    fi

    if ! command_exists jq; then
        missing_deps+=("jq")
    fi

    if ! command_exists sha256sum && ! command_exists shasum; then
        missing_deps+=("sha256sum or shasum")
    fi

    # Check for package-specific dependencies
    case "$package_type" in
        tar.gz)
            if ! command_exists tar; then
                missing_deps+=("tar")
            fi
            ;;
        deb)
            if ! command_exists dpkg; then
                missing_deps+=("dpkg")
            fi
            ;;
        rpm)
            if ! command_exists rpm; then
                missing_deps+=("rpm")
            fi
            ;;
        apk)
            if ! command_exists apk; then
                missing_deps+=("apk")
            fi
            ;;
    esac

    if [ ${#missing_deps[@]} -gt 0 ]; then
        print_error "Missing required dependencies: ${missing_deps[*]}"
        print_info "Please install the missing dependencies and try again."

        # Provide installation hints for common package managers
        if command_exists apt-get; then
            print_info "Ubuntu/Debian: sudo apt-get install curl jq coreutils tar"
        elif command_exists yum; then
            print_info "RHEL/CentOS: sudo yum install curl jq coreutils tar"
        elif command_exists brew; then
            print_info "macOS: brew install curl jq coreutils gnu-tar"
        fi

        exit 1
    fi
}

# Function to download file
download_file() {
    local url="$1"
    local output="$2"

    if command_exists curl; then
        if ! curl -fsSL -o "$output" "$url"; then
            print_error "Failed to download from $url"
            return 1
        fi
    elif command_exists wget; then
        if ! wget -q -O "$output" "$url"; then
            print_error "Failed to download from $url"
            return 1
        fi
    else
        print_error "No download tool available (curl or wget)"
        return 1
    fi

    # Verify file was created and has content
    if [ ! -f "$output" ] || [ ! -s "$output" ]; then
        print_error "Downloaded file is empty or doesn't exist: $output"
        return 1
    fi

    return 0
}

# Function to get latest release info
get_latest_release() {
    local api_url="https://api.github.com/repos/$REPO/releases/latest"
    local temp_file
    temp_file=$(mktemp)

    print_info "Fetching latest release information..." >&2

    if ! download_file "$api_url" "$temp_file"; then
        print_error "Failed to fetch release information"
        rm -f "$temp_file"
        exit 1
    fi

    if ! jq -e . "$temp_file" >/dev/null 2>&1; then
        print_error "Invalid JSON response from GitHub API"
        cat "$temp_file" >&2
        rm -f "$temp_file"
        exit 1
    fi

    echo "$temp_file"
}

# Function to verify checksum
verify_checksum() {
    local file="$1"
    local checksums_file="$2"
    local filename
    filename=$(basename "$file")

    print_info "Verifying checksum for $filename..."

    # Extract expected checksum
    local expected_checksum
    expected_checksum=$(grep "$filename" "$checksums_file" | awk '{print $1}')

    if [ -z "$expected_checksum" ]; then
        print_warning "No checksum found for $filename in checksums file"
        return 1
    fi

    # Calculate actual checksum
    local actual_checksum
    if command_exists sha256sum; then
        actual_checksum=$(sha256sum "$file" | awk '{print $1}')
    elif command_exists shasum; then
        actual_checksum=$(shasum -a 256 "$file" | awk '{print $1}')
    else
        print_error "No checksum tool available"
        return 1
    fi

    if [ "$expected_checksum" = "$actual_checksum" ]; then
        print_success "Checksum verification passed"
        return 0
    else
        print_error "Checksum verification failed!"
        print_error "Expected: $expected_checksum"
        print_error "Actual:   $actual_checksum"
        return 1
    fi
}

# Function to verify GPG signature (optional)
verify_signature() {
    local checksums_file="$1"
    local signature_file="$2"

    if ! command_exists gpg; then
        print_warning "GPG not available, skipping signature verification"
        return 0
    fi

    print_info "Verifying GPG signature..."

    if gpg --verify "$signature_file" "$checksums_file" 2>/dev/null; then
        print_success "GPG signature verification passed"
        return 0
    else
        print_warning "GPG signature verification failed or key not trusted"
        print_info "You may need to import the signing key first"
        return 1
    fi
}

# Function to install binary
install_binary() {
    local binary_file="$1"
    local install_path="$INSTALL_DIR/$BINARY_NAME"

    print_info "Installing $BINARY_NAME to $install_path..."

    # Create install directory if it doesn't exist
    if [ ! -d "$INSTALL_DIR" ]; then
        if ! run_with_privilege mkdir -p "$INSTALL_DIR"; then
            print_error "Failed to create install directory: $INSTALL_DIR"
            print_info "Try running with sudo or set INSTALL_DIR to a writable location"
            exit 1
        fi
    fi

    # Copy and set permissions
    if ! run_with_privilege cp "$binary_file" "$install_path"; then
        print_error "Failed to copy binary to $install_path"
        print_info "Try running with sudo or set INSTALL_DIR to a writable location"
        exit 1
    fi

    run_with_privilege chmod +x "$install_path"
    print_success "$BINARY_NAME installed successfully to $install_path"
}

# Function to extract and install from tar.gz
install_from_tarball() {
    local tarball_file="$1"
    local temp_dir="$2"
    local extract_dir="${temp_dir}/extract"

    print_info "Extracting tarball..."
    mkdir -p "$extract_dir"

    if ! tar -xzf "$tarball_file" -C "$extract_dir"; then
        print_error "Failed to extract tarball"
        return 1
    fi

    # Find the binary in the extracted files
    local binary_file
    binary_file=$(find "$extract_dir" -name "$BINARY_NAME" -type f | head -n1)

    if [ -z "$binary_file" ]; then
        # Try common variations
        binary_file=$(find "$extract_dir" -name "tigrisfs*" -type f -executable | head -n1)
    fi

    if [ -z "$binary_file" ]; then
        print_error "Binary not found in extracted files"
        return 1
    fi

    install_binary "$binary_file"
}

# Function to run command with sudo if not root
run_with_privilege() {
    if [ "$EUID" -eq 0 ]; then
        "$@"
    else
        sudo "$@"
    fi
}

# Function to install package using system package manager
install_package() {
    local package_file="$1"
    local package_type="$2"

    print_info "Installing $package_type package..."

    case "$package_type" in
        deb)
            if ! run_with_privilege dpkg -i "$package_file" 2>/dev/null; then
                print_info "Package installation failed, trying with apt-get to fix dependencies..."
                if command_exists apt-get; then
                   run_with_privilege apt-get install -f -y
                fi
            fi
            ;;
        rpm)
            if command_exists dnf; then
               run_with_privilege dnf install -y "$package_file"
            elif command_exists yum; then
               run_with_privilege yum install -y "$package_file"
            else
               run_with_privilege rpm -i "$package_file"
            fi
            ;;
        apk)
            run_with_privilege apk add --allow-untrusted "$package_file"
            ;;
        *)
            print_error "Unsupported package type: $package_type"
            return 1
            ;;
    esac

    print_success "Package installed successfully"
}

# Main installation function
main() {
    print_info "TigrisFS Installation Script"
    print_info "Repository: https://github.com/$REPO"

    # Detect system
    local os arch package_type
    os=$(detect_os)
    arch=$(detect_arch)
    package_type=$(detect_package_preference "$os")

    print_info "Detected system: $os/$arch"
    print_info "Preferred package type: $package_type"

    # Check dependencies
    check_dependencies "$package_type"

    # Get latest release info
    local release_file
    release_file=$(get_latest_release)

    if [ ! -f "$release_file" ]; then
        print_error "Failed to get release information"
        exit 1
    fi

    local tag_name
    tag_name=$(jq -r '.tag_name' "$release_file" 2>/dev/null)

    if [ -z "$tag_name" ] || [ "$tag_name" = "null" ]; then
        print_error "Could not parse release tag from GitHub API response"
        print_info "API Response:"
        head -n 10 "$release_file" >&2
        rm -f "$release_file"
        exit 1
    fi

    print_info "Latest release: $tag_name"

    # Determine package filename based on the actual release format
    local package_filename
    case "$package_type" in
        tar.gz)
            package_filename="tigrisfs_${tag_name#v}_${os}_${arch}.tar.gz"
            ;;
        deb)
            package_filename="tigrisfs_${tag_name#v}_${os}_${arch}.deb"
            ;;
        rpm)
            package_filename="tigrisfs_${tag_name#v}_${os}_${arch}.rpm"
            ;;
        apk)
            package_filename="tigrisfs_${tag_name#v}_${os}_${arch}.apk"
            ;;
    esac

    # Find download URL for package
    local package_url
    package_url=$(jq -r --arg name "$package_filename" '.assets[] | select(.name == $name) | .browser_download_url' "$release_file")

    if [ -z "$package_url" ] || [ "$package_url" = "null" ]; then
        # Try fallback to tar.gz if preferred package type not found
        if [ "$package_type" != "tar.gz" ]; then
            print_warning "Preferred package type ($package_type) not found, falling back to tar.gz"
            package_type="tar.gz"
            package_filename="tigrisfs_${tag_name#v}_${os}_${arch}.tar.gz"
            package_url=$(jq -r --arg name "$package_filename" '.assets[] | select(.name == $name) | .browser_download_url' "$release_file")
        fi

        if [ -z "$package_url" ] || [ "$package_url" = "null" ]; then
            print_error "Package not found for $os/$arch"
            print_info "Available assets:"
            jq -r '.assets[].name' "$release_file" | sed 's/^/  - /'
            rm -f "$release_file"
            exit 1
        fi
    fi

    # Find checksums and signature URLs
    local checksums_url signature_url
    checksums_url=$(jq -r '.assets[] | select(.name == "checksums.txt") | .browser_download_url' "$release_file")
    signature_url=$(jq -r '.assets[] | select(.name == "checksums.sig") | .browser_download_url' "$release_file")

    rm -f "$release_file"

    # Create temporary directory
    local temp_dir
    temp_dir=$(mktemp -d)

    # Cleanup function
    cleanup() {
        rm -rf "$temp_dir"
    }
    trap cleanup EXIT

    # Download files
    local package_file="${temp_dir}/${package_filename}"
    local checksums_file="${temp_dir}/checksums.txt"
    local signature_file="${temp_dir}/checksums.sig"

    print_info "Downloading $package_filename..."
    download_file "$package_url" "$package_file"

    # Download checksums if available
    if [ -n "$checksums_url" ] && [ "$checksums_url" != "null" ]; then
        print_info "Downloading checksums.txt..."
        download_file "$checksums_url" "$checksums_file"

        # Verify checksum
        if ! verify_checksum "$package_file" "$checksums_file"; then
            print_error "Checksum verification failed. Aborting installation."
            exit 1
        fi

        # Download and verify signature if available
        if [ -n "$signature_url" ] && [ "$signature_url" != "null" ]; then
            print_info "Downloading checksums.sig..."
            download_file "$signature_url" "$signature_file"
            verify_signature "$checksums_file" "$signature_file"
        fi
    else
        print_warning "Checksums not available, skipping verification"
    fi

    # Install based on package type
    case "$package_type" in
        tar.gz)
            install_from_tarball "$package_file" "$temp_dir"
            ;;
        deb|rpm|apk)
            # For system packages, we need root privileges
#            if [ "$EUID" -ne 0 ] && [ -z "$FORCE_TARBALL" ]; then
#                print_info "System package installation requires root privileges."
#                print_info "Please run with sudo, or set FORCE_TARBALL=1 to use tarball installation instead."
#                exit 1
#            fi
            install_package "$package_file" "$package_type"
            ;;
    esac

    # Verify installation
    if command_exists "$BINARY_NAME"; then
        print_success "Installation completed successfully!"
        print_info "Run '$BINARY_NAME --help' to get started"

        # Show version if possible
        if "$BINARY_NAME" --version >/dev/null 2>&1; then
            local version
			version=$("$BINARY_NAME" --version 2>&1| head -n1 | cut -d ' ' -f 3)
            print_info "Installed version: $version"
        fi
    else
        if [ "$package_type" = "tar.gz" ]; then
            print_warning "Installation completed, but $BINARY_NAME is not in PATH"
            print_info "Make sure $INSTALL_DIR is in your PATH, or run: export PATH=\"$INSTALL_DIR:\$PATH\""
        else
            print_warning "Package installed, but $BINARY_NAME may not be immediately available"
            print_info "Try opening a new terminal or running: hash -r"
        fi
    fi
}

# Show help
show_help() {
    cat << EOF
TigrisFS Installation Script

USAGE:
    $0 [OPTIONS]

OPTIONS:
    -h, --help          Show this help message
    --install-dir DIR   Installation directory (default: /usr/local/bin)
    --force-tarball     Force tarball installation instead of system packages
    --package-type TYPE Force specific package type (tar.gz, deb, rpm, apk)

ENVIRONMENT VARIABLES:
    INSTALL_DIR         Installation directory (default: /usr/local/bin)
    FORCE_TARBALL       Set to 1 to force tarball installation

EXAMPLES:
    # Install using system package manager (requires sudo)
    sudo $0

    # Install tarball to default location
    $0 --force-tarball

    # Install to custom directory using tarball
    $0 --force-tarball --install-dir /usr/bin

    # Install to user directory
    INSTALL_DIR=~/.local/bin $0 --force-tarball

    # Force specific package type
    sudo $0 --package-type deb

PACKAGE TYPES:
    - System packages (deb, rpm, apk) install system-wide and require sudo
    - Tarball (tar.gz) can install to user directories without sudo
    - Script automatically detects the best package type for your system

EOF
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help)
            show_help
            exit 0
            ;;
        --install-dir)
            INSTALL_DIR="$2"
            shift 2
            ;;
        --force-tarball)
            FORCE_TARBALL=1
            shift
            ;;
        --package-type)
            FORCE_PACKAGE_TYPE="$2"
            case "$FORCE_PACKAGE_TYPE" in
                tar.gz|deb|rpm|apk)
                    ;;
                *)
                    print_error "Invalid package type: $FORCE_PACKAGE_TYPE"
                    print_info "Supported types: tar.gz, deb, rpm, apk"
                    exit 1
                    ;;
            esac
            shift 2
            ;;
        *)
            print_error "Unknown option: $1"
            show_help
            exit 1
            ;;
    esac
done

# Run main function
main
