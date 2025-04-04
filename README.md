# Chainguard Golden Images Pipeline Example

## Goals

- Demonstrate ingestion pipeline for Chainguard images into a Golden Images repostitory
- Assumes Platform Engineer perspective
- Demonstrate best practices ie leveraging tools like digestabot, sha's instead of tags, etc

## Non-Goals
- This is not meant all encompasing but rather provide a "what could be" example of a potential pipeline


## Current State

Pipeline example for ingesting Python (dev and distroless) with image scanning (via grype), adding artifactory package mirroring and pushing to Artifact Registry.

## To Do

- Add Cosign Validation scanning
- Optimize Pipeline, only trigger based on changes in certain dirs, better organize jobs
- Add FIPS image validation
- Add application image validaton
- Implement Incert example
- Chibbies?