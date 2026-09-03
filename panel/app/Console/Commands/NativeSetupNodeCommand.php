<?php

namespace Pterodactyl\Console\Commands;

use Illuminate\Console\Command;
use Pterodactyl\Models\Allocation;
use Pterodactyl\Models\Node;
use Pterodactyl\Services\Nodes\NodeCreationService;

/**
 * One-shot helper for native (Docker-free) deployments: creates a node with a
 * port pool and prints the ptero-native daemon config snippet.
 */
class NativeSetupNodeCommand extends Command
{
    protected $signature = 'p:native:setup-node
                            {--name= : Node name (default: native-node)}
                            {--fqdn=127.0.0.1 : Node FQDN or IP}
                            {--listen=8080 : Daemon listen port}
                            {--scheme=http : http or https}
                            {--ports=20200-20250 : Allocation port range for servers}
                            {--ip=0.0.0.0 : Allocation bind IP}
                            {--print-key : Also print a fresh client API key for the first admin}';

    protected $description = 'Create a node + allocation pool for the native runtime daemon';

    public function handle(NodeCreationService $creationService): int
    {
        $name = $this->option('name') ?: 'native-node';
        $fqdn = (string) $this->option('fqdn');
        $listen = (int) $this->option('listen');
        $scheme = (string) $this->option('scheme');
        $ports = (string) $this->option('ports');
        $ip = (string) $this->option('ip');

        if (Node::query()->where('name', $name)->exists()) {
            $this->warn("Node '$name' already exists — aborting (nothing was changed).");

            return self::FAILURE;
        }

        // resolve a public location or create one
        $location = \Pterodactyl\Models\Location::query()->first();
        if (!$location) {
            $location = \Pterodactyl\Models\Location::query()->create([
                'long' => 'Native',
                'short' => 'native',
            ]);
            $this->info('Created location: native');
        }

        [$portStart, $portEnd] = array_map('intval', explode('-', $ports) + [null, null]);

        // NodeCreationService::handle() generates uuid, daemon_token and
        // daemon_token_id itself — never craft daemon credentials manually here.
        $data = [
            'public' => true,
            'name' => $name,
            'location_id' => $location->id,
            'fqdn' => $fqdn,
            'scheme' => $scheme,
            'behind_proxy' => false,
            'memory' => 0,
            'memory_overallocate' => 0,
            'disk' => 0,
            'disk_overallocate' => 0,
            'upload_size' => 100,
            'daemonListen' => $listen,
            'daemonSFTP' => 2022,
            'daemonBase' => '/var/lib/ptero-native/volumes',
        ];

        try {
            $node = $creationService->handle($data);
        } catch (\Throwable $e) {
            $this->error('Node creation failed: ' . $e->getMessage());

            return self::FAILURE;
        }

        // allocation pool
        $created = 0;
        for ($port = $portStart; $port <= $portEnd; $port++) {
            Allocation::query()->create([
                'node_id' => $node->id,
                'ip' => $ip,
                'port' => $port,
                'ip_alias' => null,
                'server_id' => null,
                'notes' => null,
            ]);
            $created++;
        }

        $this->info("Node '$name' created (id={$node->id}) with $created allocations ($ip:$portStart-$portEnd)");
        $this->line('');
        $this->line('Add this to /etc/ptero-native/config.yml on the daemon host:');
        $this->line('----------------------------------------------------------');
        $this->line('panel:');
        $this->line('  url: ' . rtrim(config('app.url'), '/'));
        $this->line('  token: "' . $node->daemon_token_id . '.' . $node->getDecryptedKey() . '"');
        $this->line('daemon:');
        $this->line('  listen: 0.0.0.0:' . $listen);
        $this->line('  token_id: "' . $node->daemon_token_id . '"');
        $this->line('  token: "' . $node->getDecryptedKey() . '"');
        $this->line('----------------------------------------------------------');

        if ($this->option('print-key')) {
            $user = \Pterodactyl\Models\User::query()->where('root_admin', true)->first();
            if ($user) {
                $token = $user->createToken('native runtime tooling');
                $this->line('API key (copy now, shown once): ' . $token->plainTextToken);
            }
        }

        return self::SUCCESS;
    }
}
