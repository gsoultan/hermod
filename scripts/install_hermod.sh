#!/bin/bash

# Exit on error
set -e

echo "Starting Hermod Installation..."

# 1. Update system and install dependencies
echo "Updating system and installing dependencies (Go, Node.js, NPM)..."
sudo apt-get update
sudo apt-get install -y golang-go nodejs npm git build-essential openssl

# 2. Setup Hermod Directory and User
HERMOD_DIR="/opt/hermod"
HERMOD_USER="hermod"
DATA_DIR="/var/lib/hermod"

if ! id -u $HERMOD_USER > /dev/null 2>&1; then
    sudo useradd -m -r -s /bin/false $HERMOD_USER
fi

sudo mkdir -p $HERMOD_DIR
sudo mkdir -p $DATA_DIR
sudo chown $HERMOD_USER:$HERMOD_USER $DATA_DIR

# 3. Clone/Copy Source Code (Assuming script is run from inside the repo)
echo "Preparing source code..."
# If this is a fresh install from a repo:
# sudo git clone https://github.com/youruser/hermod.git $HERMOD_DIR/src
# For now, we assume we are in the source directory
sudo cp -r . $HERMOD_DIR/src
sudo chown -R $HERMOD_USER:$HERMOD_USER $HERMOD_DIR/src

# 4. Build Hermod
echo "Building Hermod (this may take a few minutes)..."
cd $HERMOD_DIR/src

# Build UI and Go binary
# We run the build process. Ensure we have enough permissions.
go run cmd/hermod/main.go --build-ui
go build -o hermod cmd/hermod/main.go

sudo mv hermod /usr/local/bin/hermod
sudo chmod +x /usr/local/bin/hermod

# 5. Create Systemd Service
echo "Configuring systemd service..."

# Generate a random master key if not provided
MASTER_KEY=$(openssl rand -base64 32)

cat <<EOF | sudo tee /etc/systemd/system/hermod.service
[Unit]
Description=Hermod Messaging System
After=network.target

[Service]
Type=simple
User=$HERMOD_USER
WorkingDirectory=$DATA_DIR
Environment=HERMOD_MASTER_KEY=$MASTER_KEY
# You can customize these flags (e.g., use postgres instead of sqlite)
ExecStart=/usr/local/bin/hermod --port=8080 --db-type=sqlite --db-conn=$DATA_DIR/hermod.db
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF

# 6. Start the Service
echo "Starting Hermod service..."
sudo systemctl daemon-reload
sudo systemctl enable hermod
sudo systemctl start hermod

echo "-------------------------------------------------------"
echo "Hermod Installation Complete!"
echo "Service Status: \$(sudo systemctl is-active hermod)"
echo "UI is available at: http://\$(hostname -I | awk '{print \$1}'):8080"
echo "Master Key generated and set in /etc/systemd/system/hermod.service"
echo "-------------------------------------------------------"
