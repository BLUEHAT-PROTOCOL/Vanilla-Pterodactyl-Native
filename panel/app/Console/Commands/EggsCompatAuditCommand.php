<?php

namespace Pterodactyl\Console\Commands;

use Illuminate\Console\Command;
use Illuminate\Support\Str;
use Pterodactyl\Models\Egg;
use Pterodactyl\Models\RuntimeMapping;
use Pterodactyl\Models\RuntimeProfile;

/**
 * Classifies all eggs for native (Docker-free) compatibility and stores the
 * result in eggs.native_compat. Statuses:
 *   native      — runs with a mapped runtime as-is
 *   mapping     — docker image has a runtime mapping; startup translatable
 *   manual      — needs the egg-compat layer (config files, docker-only install bits)
 *   unsupported — requires docker features the native runtime cannot provide
 */
class EggsCompatAuditCommand extends Command
{
    protected $signature = 'p:eggs:compat-audit
                            {--fix : Update the eggs table with the computed status}';

    protected $description = 'Audit all eggs for native runtime compatibility';

    private array $mappedImages = [];

    public function handle(): int
    {
        $this->mappedImages = RuntimeMapping::query()->pluck('docker_image')->all();

        $eggs = Egg::query()->with('nest')->get();
        $counts = ['native' => 0, 'mapping' => 0, 'manual' => 0, 'unsupported' => 0];

        $rows = [];
        foreach ($eggs as $egg) {
            [$status, $notes] = $this->classify($egg);
            $counts[$status]++;

            if ($this->option('fix')) {
                $egg->forceFill([
                    'native_compat' => $status,
                    'native_notes' => $notes,
                ])->save();
            }

            $rows[] = [$egg->nest->name ?? '-', $egg->name, $egg->id, $status, Str::limit($notes ?? '', 60)];
        }

        $this->table(['Nest', 'Egg', 'ID', 'Status', 'Notes'], $rows);
        $this->info(sprintf(
            'Total: %d — native: %d, mapping: %d, manual: %d, unsupported: %d%s',
            count($rows), $counts['native'], $counts['mapping'], $counts['manual'], $counts['unsupported'],
            $this->option('fix') ? '' : ' (dry-run; use --fix to persist)'
        ));

        return self::SUCCESS;
    }

    private function classify(Egg $egg): array
    {
        $notes = [];
        $images = $egg->docker_images ?? [];
        $startup = (string) $egg->startup;
        // config_files is stored as a raw JSON string; normalize it.
        $configFiles = $egg->config_files ?? [];
        if (is_string($configFiles)) {
            $decoded = json_decode($configFiles, true);
            $configFiles = is_array($decoded) ? $decoded : [];
        }
        $installScript = (string) $egg->script_install;

        $hasMapping = false;
        foreach ((array) $images as $image => $label) {
            $key = is_string($image) ? $image : $label;
            if (in_array($key, $this->mappedImages, true) || $this->builtinMatch($key)) {
                $hasMapping = true;
            } else {
                $notes[] = "image '$key' has no runtime mapping (falls back to custom)";
            }
        }

        if ($images === []) {
            $notes[] = 'no docker images declared';
        }

        // startup must be a plain shell line (no docker RUN semantics)
        $plainStartup = true;
        if (preg_match('/\b(docker|kubectl|systemctl|service\s+\w+\s+start)\b/i', $startup)) {
            $plainStartup = false;
            $notes[] = 'startup references docker/system services';
        }

        // config files are handled by the egg-compat layer
        if (!empty($configFiles)) {
            $notes[] = count($configFiles) . ' config file(s) handled by the compat layer';
        }

        // install scripts with docker-only commands
        $dockerInstall = false;
        if (preg_match('/\b(apt-get install|dpkg|docker pull|curl -sSL https:\/\/get\.docker)\b/i', $installScript)) {
            $dockerInstall = true;
            $notes[] = 'install script may need packages outside the mapped runtime';
        }

        if (!$plainStartup) {
            $status = 'unsupported';
        } elseif ($hasMapping && empty($configFiles) && !$dockerInstall) {
            $status = 'native';
        } elseif ($hasMapping) {
            $status = 'mapping';
        } else {
            $status = 'manual';
        }

        return [$status, implode('; ', $notes) ?: 'no issues detected'];
    }

    private function builtinMatch(string $image): bool
    {
        foreach (['nodejs_', 'python_', 'java_'] as $prefix) {
            if (str_contains($image, $prefix)) {
                return true;
            }
        }

        return str_contains($image, 'yolks:debian') || str_contains($image, 'yolks:ubuntu') || str_contains($image, 'yolks:alpine');
    }
}
