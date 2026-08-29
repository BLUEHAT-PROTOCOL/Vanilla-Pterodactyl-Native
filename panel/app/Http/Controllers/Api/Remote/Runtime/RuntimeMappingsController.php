<?php

namespace Pterodactyl\Http\Controllers\Api\Remote\Runtime;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Pterodactyl\Http\Controllers\Controller;
use Pterodactyl\Models\Node;
use Pterodactyl\Models\RuntimeMapping;
use Webmozart\Assert\Assert;

/**
 * Returns the native runtime mapping table to the ptero-native daemon,
 * which uses it to translate docker images into native runtimes.
 */
class RuntimeMappingsController extends Controller
{
    public function __invoke(Request $request): JsonResponse
    {
        Assert::isInstanceOf($node = $request->attributes->get('node'), Node::class);

        $mappings = RuntimeMapping::query()
            ->with('profile:id,slug,name')
            ->get()
            ->map(fn (RuntimeMapping $m) => [
                'docker_image' => $m->docker_image,
                'runtime_version' => $m->runtime_version,
                'env_path' => $m->env_path,
                'extra_env' => $m->extra_env,
                'profile' => $m->profile?->only(['slug', 'name']),
            ])
            ->values();

        return new JsonResponse(['data' => $mappings]);
    }
}
