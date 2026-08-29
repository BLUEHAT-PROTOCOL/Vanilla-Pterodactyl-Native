# Persistence Test — Vanilla Pterodactyl Native

This file marks the successful verification of **GitHub persistent storage** for the
Vanilla Pterodactyl Native project.

## Purpose

The project was previously lost twice due to sandbox resets. Starting from this commit,
GitHub is the canonical **source of truth**: every completed sub-module and milestone is
committed and pushed immediately, so a sandbox reset can no longer destroy progress.

## What was verified

1. Repository reachable over HTTPS: `https://github.com/BLUEHAT-PROTOCOL/Vanilla-Pterodactyl-Native.git`
2. Branch `main` used as the primary branch.
3. Local commit created and pushed with token-based authentication (credential helper;
   the token itself is never stored in the remote URL, source code, or project files).
4. Remote verification performed via `git ls-remote` and a fresh anonymous clone.

## Project

Vanilla Pterodactyl Native — a Docker-free runtime hosting panel based on a minimal
additive fork of the official Pterodactyl Panel plus a native Go daemon (`ptero-native`)
that is protocol-compatible with Wings, designed for NAT VPS / restricted containers
(no CAP_NET_ADMIN, no network namespaces, systemd optional).
