[![License](https://img.shields.io/badge/license-MIT-blue)](https://opensource.org/licenses/MIT) [![Work In Progress](https://img.shields.io/badge/Status-Work%20In%20Progress-yellow)](https://guide.unitvectorylabs.com/bestpractices/status/#work-in-progress)

# gcpmetadataexplorer

A web-based interface for browsing and inspecting the GCP metadata service.

## Overview

`gcpmetadataexplorer` is a web application designed for deployment on GCP to explore data available from the GCP [metadata service](https://cloud.google.com/compute/docs/metadata/overview). The metadata service can be accessed at `http://metadata.google.internal/computeMetadata/v1` when running on a VM or container in GCP and is used to provide information about the instance, project, and service account. This application provides a user-friendly interface for browsing the metadata service and inspecting the data available including the various URLs for accessing metadata.

## ⚠️ Security Warning

The GCP metadata service is a powerful API that must not be exposed to the public internet. Improper use of this application could pose serious security risks. To ensure security, take precautions such as using [Identity-Aware Proxy](https://cloud.google.com/security/products/iap) (IAP) to restrict access.

## Usage

The latest `gcpmetadataexplorer` Docker image is available for deployment from GitHub Packages at [ghcr.io/unitvectory-labs/gcpmetadataexplorer](https://github.com/UnitVectorY-Labs/gcpmetadataexplorer/pkgs/container/gcpmetadataexplorer).

## Configuration

The application is configurable through environment variables. Below are the available configurations:

- `ALLOW_TOKENS`: Enables access to access tokens and identity tokens through the interface. This feature is disabled by default. Set to `true` to enable.
