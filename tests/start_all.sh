#!/bin/bash

# Script to start all databases and the PPROX VM
# **Run this on your LOCAL MACHINE**

# Load environment variables
source ~/pprox-setup.env

echo "=========================================="
echo "STARTING ALL INFRASTRUCTURE"
echo "=========================================="
echo ""
echo "This will start:"
echo "  1. PPROX Server (pprox-server)"
echo "  2. Cloud SQL Writer (pprox-writer-primary)"
echo "  3. Cloud SQL Reader (pprox-reader-replica)"
echo "  4. RDS Writer (pprox-writer-rds)"
echo ""

# Start PPROX VM first
echo "1. Starting PPROX VM..."
echo "   Instance: pprox-server"
echo "------------------------------------------"
gcloud compute instances start pprox-server \
    --zone=$GCP_ZONE \
    --project=$GCP_PROJECT
if [ $? -eq 0 ]; then
    echo "✅ PPROX Server started"
    echo "⏳ Waiting for server to be ready..."
    sleep 10
else
    echo "❌ Failed to start PPROX Server"
fi
echo ""

# Start Cloud SQL Writer
echo "2. Starting Cloud SQL Writer..."
echo "   Instance: pprox-writer-primary"
echo "------------------------------------------"
gcloud sql instances patch pprox-writer-primary \
    --activation-policy=ALWAYS \
    --project=$GCP_PROJECT
if [ $? -eq 0 ]; then
    echo "✅ Cloud SQL Writer started"
else
    echo "❌ Failed to start Cloud SQL Writer"
fi
echo ""

# Start Cloud SQL Reader
echo "3. Starting Cloud SQL Reader..."
echo "   Instance: pprox-reader-replica"
echo "------------------------------------------"
gcloud sql instances patch pprox-reader-replica \
    --activation-policy=ALWAYS \
    --project=$GCP_PROJECT
if [ $? -eq 0 ]; then
    echo "✅ Cloud SQL Reader started"
else
    echo "❌ Failed to start Cloud SQL Reader"
fi
echo ""

# Start RDS Writer
echo "4. Starting RDS Writer..."
echo "   Instance: pprox-writer-rds"
echo "------------------------------------------"
aws rds start-db-instance \
    --db-instance-identifier pprox-writer-rds \
    --region ap-southeast-1 \
    --output text
if [ $? -eq 0 ]; then
    echo "✅ RDS Writer started"
else
    echo "❌ Failed to start RDS Writer"
fi
echo ""

# Wait for services to be ready
echo "⏳ Waiting for all services to be ready..."
echo "   This may take 1-2 minutes..."
sleep 30

# Summary
echo ""
echo "=========================================="
echo "START SUMMARY"
echo "=========================================="
echo ""
echo "All infrastructure has been started."
echo ""
echo "Next steps:"
echo "  1. Wait a few moments for databases to be fully ready"
echo "  2. Check VM status: gcloud compute instances describe pprox-vm --zone=$GCP_ZONE"
echo "  3. Check pprox service: gcloud compute ssh pprox-server --zone=$GCP_ZONE --command='sudo systemctl status pprox'"
echo "  4. Test connections: ./tests/view.sh"
echo ""
echo "Note: Databases may take a few minutes to be fully available."
