#!/bin/bash

# Check if directory parameter is provided
if [ $# -ne 1 ]; then
    echo "Usage: $0 <local-directory-path>"
    echo "Example: $0 /home/user/documents"
    exit 1
fi

CURDIR=$(dirname $(readlink -f $0))
# Define local directory and container parameters
LOCAL_DIR="$1"
CONTAINER_NAME="file-server-nginx"
NGINX_HTML_DIR="/usr/share/nginx/html"  # Default web directory in Nginx container
NGINX_DEFAULT_CONFIG="$CURDIR/nginx.conf"

# Check if local directory exists
if [ ! -d "$LOCAL_DIR" ]; then
    echo "Error: Directory $LOCAL_DIR does not exist. Please check the path."
    exit 1
fi

# Stop and remove existing container with the same name (if exists)
echo "Cleaning up existing container (if any)..."
docker stop "$CONTAINER_NAME" >/dev/null 2>&1 || true
docker rm "$CONTAINER_NAME" >/dev/null 2>&1 || true

# Start Nginx container
echo "Starting Nginx file server, mapping directory: $LOCAL_DIR -> $NGINX_HTML_DIR in container"
docker run -d \
    --name "$CONTAINER_NAME" \
    -p 80:80 \
    -v "$LOCAL_DIR:$NGINX_HTML_DIR:ro" \
    -v "$NGINX_DEFAULT_CONFIG:/etc/nginx/conf.d/default.conf:ro" \
    nginx:latest

# Check startup result
if [ $? -eq 0 ]; then
    echo "Nginx file server started successfully!"
    echo "Access address: http://localhost or http://your-local-ip"
    echo "Container name: $CONTAINER_NAME"
else
    echo "Error: Failed to start Nginx container. Please check if Docker is running or if port 80 is occupied."
    exit 1
fi

