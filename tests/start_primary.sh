#!/bin/bash

# Script to start the Cloud SQL primary instance (pprox-writer-primary)
# Use this to restart the primary after testing failover
#
# **IMPORTANT: Run this on your LOCAL MACHINE (not on the VM)**
# Requires gcloud CLI with appropriate permissions

# Load environment variables
source ~/pprox-setup.env

echo "=========================================="
echo "Starting Cloud SQL Primary Instance"
echo "=========================================="
echo ""
echo "Instance: pprox-writer-primary"
echo "Project: $GCP_PROJECT"
echo ""

echo "Starting pprox-writer-primary..."

gcloud sql instances patch pprox-writer-primary \
    --activation-policy=ALWAYS \
    --project=$GCP_PROJECT

if [ $? -eq 0 ]; then
    echo ""
    echo "✅ Cloud SQL primary instance started successfully"
    echo ""
    echo "⏳ Waiting for instance to be ready..."
    sleep 10
    
    # Check instance status
    STATUS=$(gcloud sql instances describe pprox-writer-primary \
        --project=$GCP_PROJECT \
        --format="value(state)")
    
    echo "Instance state: $STATUS"
    echo ""
    echo "Next steps:"
    echo "1. Wait a few moments for the instance to fully start"
    echo "2. Test connections with test_direct_connections.sh"
    echo "3. Verify that pprox can write to Cloud SQL again"
else
    echo ""
    echo "❌ Failed to start Cloud SQL primary instance"
    exit 1
fi
