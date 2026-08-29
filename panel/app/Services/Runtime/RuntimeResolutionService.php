<?php

namespace Pterodactyl\Services\Runtime;

use Pterodactyl\Models\RuntimeMapping;
use Pterodactyl\Models\RuntimeProfile;
use Pterodactyl\Models\Server;

/**
 * Resolves the native runtime for a server from its docker image using the
 * runtime_mappings table. Result is exposed to the daemon as settings.runtime
 * ("profileSlug|envPath"); Wings ignores the key entirely, keeping the fork
 * additive and backwards compatible.
 */
class RuntimeResolutionService
{
    /**
     * Returns the "slug|envPath" runtime key for a server, or empty string when
     * no mapping exists (daemon falls back to its own defaults).
     */
    public function runtimeKey(Server $server): string
    {
        $mapping = RuntimeMapping::query()
            ->select('runtime_mappings.*', 'runtime_profiles.slug')
            ->join('runtime_profiles', 'runtime_profiles.id', '=', 'runtime_mappings.profile_id')
            ->whereRaw('LOWER(docker_image) = ?', [strtolower((string) $server->image)])
            ->first();

        if (!$mapping) {
            return '';
        }

        return $mapping->slug . ($mapping->env_path ? '|' . $mapping->env_path : '|');
    }

    /**
     * Returns the profile model for a docker image (null = unmapped).
     */
    public function profileFor(string $dockerImage): ?RuntimeProfile
    {
        $mapping = RuntimeMapping::query()
            ->whereRaw('LOWER(docker_image) = ?', [strtolower(trim($dockerImage))])
            ->first();

        return $mapping?->profile;
    }
}
