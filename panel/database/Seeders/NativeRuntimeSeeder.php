<?php

namespace Database\Seeders;

use Illuminate\Database\Seeder;
use Illuminate\Support\Str;
use Pterodactyl\Models\RuntimeMapping;
use Pterodactyl\Models\RuntimeProfile;

/**
 * Seeds the default native runtime profiles and the docker-image → runtime
 * mappings for the official pterodactyl/yolks images.
 */
class NativeRuntimeSeeder extends Seeder
{
    public function run(): void
    {
        $profiles = [
            ['slug' => 'node20', 'name' => 'Node.js 20 (LTS)', 'binary' => 'node', 'binary_args' => null, 'description' => 'Native Node.js 20 runtime', 'supported_versions' => ['20'], 'default_image' => 'ghcr.io/pterodactyl/yolks:nodejs_20'],
            ['slug' => 'node22', 'name' => 'Node.js 22 (LTS)', 'binary' => 'node', 'binary_args' => null, 'description' => 'Native Node.js 22 runtime', 'supported_versions' => ['22'], 'default_image' => 'ghcr.io/pterodactyl/yolks:nodejs_22'],
            ['slug' => 'node24', 'name' => 'Node.js 24 (LTS)', 'binary' => 'node', 'binary_args' => null, 'description' => 'Native Node.js 24 runtime', 'supported_versions' => ['24'], 'default_image' => 'ghcr.io/pterodactyl/yolks:nodejs_24'],
            ['slug' => 'python311', 'name' => 'Python 3.11', 'binary' => 'python3', 'binary_args' => null, 'description' => 'Native Python 3.11 runtime', 'supported_versions' => ['3.11'], 'default_image' => 'ghcr.io/pterodactyl/yolks:python_3.11'],
            ['slug' => 'python312', 'name' => 'Python 3.12', 'binary' => 'python3', 'binary_args' => null, 'description' => 'Native Python 3.12 runtime', 'supported_versions' => ['3.12'], 'default_image' => 'ghcr.io/pterodactyl/yolks:python_3.12'],
            ['slug' => 'java17', 'name' => 'Java 17 (LTS)', 'binary' => 'java', 'binary_args' => null, 'description' => 'Native Java 17 runtime (Temurin)', 'supported_versions' => ['17'], 'default_image' => 'ghcr.io/pterodactyl/yolks:java_17'],
            ['slug' => 'java21', 'name' => 'Java 21 (LTS)', 'binary' => 'java', 'binary_args' => null, 'description' => 'Native Java 21 runtime (Temurin)', 'supported_versions' => ['21'], 'default_image' => 'ghcr.io/pterodactyl/yolks:java_21'],
            ['slug' => 'static', 'name' => 'Static file server', 'binary' => null, 'binary_args' => null, 'description' => 'Serve static content (nginx/python http.server)', 'supported_versions' => [], 'default_image' => 'ghcr.io/pterodactyl/yolks:debian'],
            ['slug' => 'custom', 'name' => 'Custom command', 'binary' => null, 'binary_args' => null, 'description' => 'Run the egg startup with the system PATH', 'supported_versions' => [], 'default_image' => null],
            ['slug' => 'go', 'name' => 'Go (pre-built binaries)', 'binary' => null, 'binary_args' => null, 'description' => 'Run pre-compiled Go binaries', 'supported_versions' => [], 'default_image' => null],
            ['slug' => 'rust', 'name' => 'Rust (pre-built binaries)', 'binary' => null, 'binary_args' => null, 'description' => 'Run pre-compiled Rust binaries', 'supported_versions' => [], 'default_image' => null],
        ];

        $paths = [
            'node20' => '/opt/runtimes/node20/bin',
            'node22' => '/opt/runtimes/node22/bin',
            'node24' => '/opt/runtimes/node24/bin',
            'python311' => '/opt/runtimes/python311/bin',
            'python312' => '/opt/runtimes/python312/bin',
            'java17' => '/opt/runtimes/java17/bin',
            'java21' => '/opt/runtimes/java21/bin',
        ];

        $profileIds = [];
        foreach ($profiles as $p) {
            $profile = RuntimeProfile::query()->firstOrCreate(
                ['slug' => $p['slug']],
                [
                    'uuid' => Str::uuid()->toString(),
                    'name' => $p['name'],
                    'binary' => $p['binary'],
                    'binary_args' => $p['binary_args'],
                    'description' => $p['description'],
                    'supported_versions' => $p['supported_versions'],
                    'default_image' => $p['default_image'],
                ]
            );
            $profileIds[$p['slug']] = $profile->id;
        }

        $mappings = [
            ['ghcr.io/pterodactyl/yolks:nodejs_18', 'node20', '20'],
            ['ghcr.io/pterodactyl/yolks:nodejs_20', 'node20', '20'],
            ['ghcr.io/pterodactyl/yolks:nodejs_22', 'node22', '22'],
            ['ghcr.io/pterodactyl/yolks:nodejs_24', 'node24', '24'],
            ['ghcr.io/ptero-eggs/yolks:nodejs_24', 'node24', '24'],
            ['ghcr.io/pterodactyl/yolks:python_3.11', 'python311', '3.11'],
            ['ghcr.io/pterodactyl/yolks:python_3.12', 'python312', '3.12'],
            ['ghcr.io/pterodactyl/yolks:java_17', 'java17', '17'],
            ['ghcr.io/pterodactyl/yolks:java_21', 'java21', '21'],
            ['ghcr.io/pterodactyl/yolks:java_22', 'java21', '21'],
            ['ghcr.io/pterodactyl/yolks:debian', 'static', null],
            ['ghcr.io/pterodactyl/yolks:ubuntu', 'static', null],
            ['ghcr.io/pterodactyl/yolks:alpine', 'static', null],
            ['ghcr.io/pterodactyl/yolks:go_1.21', 'go', null],
            ['ghcr.io/pterodactyl/yolks:go_1.22', 'go', null],
        ];

        foreach ($mappings as [$image, $slug, $version]) {
            RuntimeMapping::query()->firstOrCreate(
                ['docker_image' => $image],
                [
                    'profile_id' => $profileIds[$slug],
                    'runtime_version' => $version,
                    'env_path' => $paths[$slug] ?? null,
                    'extra_env' => null,
                ]
            );
        }
    }
}
