#!/bin/bash

# Script to stop all databases and the PPROX VM
# **Run this on your LOCAL MACHINE**

# Load environment variables
source ~/pprox-setup.env

echo "=========================================="
echo "STOPPING ALL INFRASTRUCTURE"
echo "=========================================="
echo ""
echo "This will stop:"
echo "  1. Cloud SQL Writer (pprox-writer-primary)"
echo "  2. Cloud SQL Reader (pprox-reader-replica)"
echo "  3. RDS Writer (pprox-writer-rds)"
echo "  4. PPROX Server (pprox-server)"
echo ""

read -p "Are you sure you want to stop everything? (yes/no): " confirm
if [ "$confirm" != "yes" ]; then
    echo "Cancelled."
    exit 0
fi

echo ""

# Stop Cloud SQL Writer
echo "1. Stopping Cloud SQL Writer..."
echo "   Instance: pprox-writer-primary"
echo "------------------------------------------"
gcloud sql instances patch pprox-writer-primary \
    --activation-policy=NEVER \
    --project=$GCP_PROJECT
if [ $? -eq 0 ]; then
    echo "✅ Cloud SQL Writer stopped"
else
    echo "❌ Failed to stop Cloud SQL Writer"
fi
echo ""

# Stop Cloud SQL Reader
echo "2. Stopping Cloud SQL Reader..."
echo "   Instance: pprox-reader-replica"
echo "------------------------------------------"
gcloud sql instances patch pprox-reader-replica \
    --activation-policy=NEVER \
    --project=$GCP_PROJECT
if [ $? -eq 0 ]; then
    echo "✅ Cloud SQL Reader stopped"
else
    echo "❌ Failed to stop Cloud SQL Reader"
fi
echo ""

# Stop RDS Writer
echo "3. Stopping RDS Writer..."
echo "   Instance: pprox-writer-rds"
echo "------------------------------------------"
aws rds stop-db-instance \
    --db-instance-identifier pprox-writer-rds \
    --region ap-southeast-1 \
    --output text
if [ $? -eq 0 ]; then
    echo "✅ RDS Writer stopped"
else
    echo "❌ Failed to stop RDS Writer"
fi
echo ""

# Stop PPROX VM
echo "4. Stopping PPROX VM..."
echo "   Instance: pprox-vm"
echo "------------------------------------------"
gcloud compute instances stop pprox-server \
    --zone=$GCP_ZONE \
    --project=$GCP_PROJECT
if [ $? -eq 0 ]; then
    echo "✅ PPROX Server stopped"
else
    echo "❌ Failed to stop PPROX Server"
fi
echo ""

# Summary
echo "=========================================="
echo "STOP SUMMARY"
echo "=========================================="
echo ""
echo "All infrastructure has been stopped."
echo ""
echo "To start everything again, run:"
echo "  ./tests/start_all.sh"
echo ""
echo "Note: RDS may take a few minutes to fully stop."
