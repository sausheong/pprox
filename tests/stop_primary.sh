#!/bin/bash

# Script to stop the Cloud SQL primary instance (pprox-writer-primary)
# This simulates a primary database failure for testing pprox failover
#
# **IMPORTANT: Run this on your LOCAL MACHINE (not on the VM)**
# Requires gcloud CLI with appropriate permissions

# Load environment variables
source ~/pprox-setup.env

echo "=========================================="
echo "Stopping Cloud SQL Primary Instance"
echo "=========================================="
echo ""
echo "Instance: pprox-writer-primary"
echo "Project: $GCP_PROJECT"
echo ""
echo "⚠️  WARNING: This will stop the primary Cloud SQL instance!"
echo "   pprox should automatically failover to RDS writer"
echo ""
read -p "Are you sure you want to continue? (yes/no): " confirm

if [ "$confirm" != "yes" ]; then
    echo "Operation cancelled."
    exit 0
fi

echo ""
echo "Stopping pprox-writer-primary..."

gcloud sql instances patch pprox-writer-primary \
    --activation-policy=NEVER \
    --project=$GCP_PROJECT

if [ $? -eq 0 ]; then
    echo ""
    echo "✅ Cloud SQL primary instance stopped successfully"
    echo ""
    echo "Next steps:"
    echo "1. Test that pprox continues to work (writes should go to RDS)"
    echo "2. Run test_addrows.sql to add new data"
    echo "3. Verify reads still work from the replica"
    echo ""
    echo "To restart the primary later, run:"
    echo "  gcloud sql instances patch pprox-writer-primary --activation-policy=ALWAYS --project=$GCP_PROJECT"
else
    echo ""
    echo "❌ Failed to stop Cloud SQL primary instance"
    exit 1
fi
