<?php

/**
 * E2E bootstrap: creates (idempotently) the admin user, an application API key,
 * a node with a token pair + allocation pool for the native daemon.
 *
 * Usage: php bootstrap-e2e.php <base-dir-for-state>
 * Prints a JSON blob with all secrets/URLs the harness needs.
 */

use Illuminate\Contracts\Console\Kernel;
use Illuminate\Contracts\Encryption\Encrypter;
use Pterodactyl\Models\ApiKey;
use Pterodactyl\Models\Allocation;
use Pterodactyl\Models\Node;
use Pterodactyl\Models\User;

require __DIR__ . '/../../panel/vendor/autoload.php';
$app = require __DIR__ . '/../../panel/bootstrap/app.php';
$kernel = $app->make(Kernel::class);
$kernel->bootstrap();

$base = $argv[1] ?? '/home/z/e2e';

// --- admin user -----------------------------------------------------------
// NOTE: User::$fillable intentionally excludes uuid upstream; forceCreate is
// required (mirrors upstream UserCreationService force-fill path).
$admin = User::query()->where('email', 'admin@example.com')->first();
if (!$admin) {
    $admin = User::forceCreate([
        'external_id' => \Ramsey\Uuid\Uuid::uuid4()->toString(),
        'uuid' => \Ramsey\Uuid\Uuid::uuid4()->toString(),
        'username' => 'admin',
        'email' => 'admin@example.com',
        'name_first' => 'E2E',
        'name_last' => 'Admin',
        'password' => \Illuminate\Support\Facades\Hash::make('e2epassword'),
        'root_admin' => true,
        'language' => 'en',
    ]);
}

// --- application API key (sanctum personal access token) -------------------
$existingKey = ApiKey::query()
    ->where('user_id', $admin->id)
    ->where('key_type', ApiKey::TYPE_ACCOUNT)
    ->where('memo', 'e2e')
    ->first();

$stored = $base . '/e2e-token.txt';
if ($existingKey && file_exists($stored)) {
    $token = trim(file_get_contents($stored));
} else {
    $newToken = $admin->createToken('e2e', null);
    // Pterodactyl wires format: <identifier><plain> (see ApiKey::findToken).
    $token = $newToken->accessToken->identifier . $newToken->plainTextToken;
    file_put_contents($stored, $token);
}

// --- node + allocations ----------------------------------------------------
// NOTE: node token columns are encrypted (upstream parity) and the camelCase
// columns must match the nodes table schema exactly.
$node = Node::query()->where('name', 'native-e2e')->first();
if (!$node) {
    $location = \Pterodactyl\Models\Location::query()->firstOrCreate(
        ['short' => 'native'],
        ['long' => 'Native']
    );
    $node = Node::forceCreate([
        'public' => true,
        'name' => 'native-e2e',
        'location_id' => $location->id,
        'fqdn' => '127.0.0.1',
        'scheme' => 'http',
        'behind_proxy' => false,
        'maintenance_mode' => false,
        'memory' => 1024,
        'memory_overallocate' => 0,
        'disk' => 10240,
        'disk_overallocate' => 0,
        'upload_size' => 100,
        'daemonListen' => 18080,
        'daemonSFTP' => 2022,
        'daemonBase' => '/home/z/e2e/daemon-data/volumes',
        'daemon_token_id' => \Illuminate\Support\Str::random(Node::DAEMON_TOKEN_ID_LENGTH),
        'daemon_token' => app(Encrypter::class)->encrypt(\Illuminate\Support\Str::random(Node::DAEMON_TOKEN_LENGTH)),
    ]);

    // allocation pool
    $rows = [];
    foreach (range(20100, 20120) as $port) {
        $rows[] = [
            'node_id' => $node->id,
            'ip' => '127.0.0.1',
            'port' => $port,
            'ip_alias' => null,
            'server_id' => null,
            'notes' => null,
        ];
    }
    Allocation::query()->insert($rows);
}

echo json_encode([
    'panel_url' => 'http://127.0.0.1:8000',
    'admin_email' => 'admin@example.com',
    'admin_password' => 'e2epassword',
    'api_token' => $token,
    'node' => [
        'id' => $node->id,
        'uuid' => $node->uuid,
        'daemon_token_id' => $node->daemon_token_id,
        'daemon_token' => $node->getDecryptedKey(),
        'listen' => $node->daemonListen,
        'fqdn' => $node->fqdn,
    ],
], JSON_PRETTY_PRINT | JSON_UNESCAPED_SLASHES) . "\n";
