#!/bin/bash

# Komari Theme Build Script
# This script builds the theme package locally

set -e  # Exit on any error

echo "Building Komari Theme Package..."

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${NC} $1"
}

print_success() {
    echo -e "${GREEN} $1${NC}"
}

print_warning() {
    echo -e "${YELLOW} $1${NC}"
}

print_error() {
    echo -e "${RED}[ERROR] $1${NC}"
}

# Check if required commands exist
check_dependencies() {
    print_status "Checking dependencies..."

    if ! command -v node &> /dev/null; then
        print_error "Node.js is not installed"
        exit 1
    fi

    if ! command -v npm &> /dev/null; then
        print_error "npm is not installed"
        exit 1
    fi

    if ! command -v zip &> /dev/null; then
        print_error "zip is not installed"
        exit 1
    fi

    print_success "All dependencies are available"
}

# Install dependencies
install_dependencies() {
    print_status "Installing dependencies..."
    npm install
    print_success "Dependencies installed"
}

# Build the project
build_project() {
    print_status "Building project..."
    npm run build
    print_success "Project built successfully"
}

# Update theme configuration
update_theme_config() {
    print_status "Updating theme configuration..."

    # Get current date in YY.MM.DD format
    VERSION_DATE=$(date +"%y.%m.%d")
    # Get commit hash (short)
    if git rev-parse --short HEAD &> /dev/null; then
        COMMIT_HASH=$(git rev-parse --short HEAD)
    else
        COMMIT_HASH="dev"
        print_warning "Not a git repository, using 'dev' as commit hash"
    fi

    echo "Version: $VERSION_DATE"
    echo "Commit: $COMMIT_HASH"

}

# Verify required files exist
verify_files() {
    print_status "Verifying required files..."

    local files_missing=false

    if [ ! -f "preview.webp" ]; then
        print_error "preview.webp not found"
        files_missing=true
    fi

    if [ ! -f "komari-theme.json" ]; then
        print_error "komari-theme.json not found"
        files_missing=true
    fi

    if [ ! -d "dist" ]; then
        print_error "dist/ directory not found"
        files_missing=true
    fi

    if [ "$files_missing" = true ]; then
        print_error "Some required files are missing"
        exit 1
    fi

    print_success "All required files found!"
}

# Create theme package
create_package() {
    print_status "Creating theme package..."

    # Get version info
    VERSION_DATE=$(date +"%y.%m.%d")
    if git rev-parse --short HEAD &> /dev/null; then
        COMMIT_HASH=$(git rev-parse --short HEAD)
    else
        COMMIT_HASH="dev"
    fi

    # Create a temporary directory for the package
    rm -rf theme-package
    mkdir -p theme-package

    # Copy required files
    cp preview.webp theme-package/
    cp komari-theme.json theme-package/
    cp -r dist/ theme-package/

    # Create zip file with version and commit hash
    ZIP_NAME="komari-theme-v${VERSION_DATE}-${COMMIT_HASH}.zip"

    cd theme-package
    zip -r "../dist/${ZIP_NAME}" .
    cd ..

    # Clean up
    rm -rf theme-package

    print_success "Created package: ${ZIP_NAME}"
    ls -la "dist/${ZIP_NAME}"
}

# Main execution
main() {
    echo "======================================"
    echo "  Komari Theme Package Builder"
    echo "======================================"
    echo

    check_dependencies
    echo

    install_dependencies
    echo

    build_project
    echo

    update_theme_config
    echo

    verify_files
    echo

    create_package
    echo

    print_success "Theme package build completed! 🎉"
    echo
    echo "You can now use the generated zip file as a theme package."
}

# Run main function
main "$@"
