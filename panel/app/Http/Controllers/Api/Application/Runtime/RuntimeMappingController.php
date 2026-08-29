<?php

namespace Pterodactyl\Http\Controllers\Api\Application\Runtime;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Pterodactyl\Http\Requests\Api\Application\Runtime\StoreRuntimeMappingRequest;
use Pterodactyl\Models\RuntimeMapping;
use Pterodactyl\Http\Controllers\Api\Application\ApplicationApiController;
use Pterodactyl\Transformers\Api\Application\RuntimeMappingTransformer;

class RuntimeMappingController extends ApplicationApiController
{
    /**
     * GET /api/application/runtime/mappings
     */
    public function index(Request $request): array
    {
        $mappings = RuntimeMapping::query()->with('profile')->paginate($request->query('per_page') ?? 50);

        return $this->fractal->collection($mappings)
            ->transformWith($this->getTransformer(RuntimeMappingTransformer::class))
            ->toArray();
    }

    /**
     * GET /api/application/runtime/resolve?image=<docker-image>
     * Resolves a docker image to a runtime profile + mapping (coverage check).
     */
    public function resolve(Request $request): JsonResponse
    {
        $image = $request->query('image', '');
        $mapping = RuntimeMapping::query()
            ->with('profile')
            ->whereRaw('LOWER(docker_image) = ?', [strtolower(trim($image))])
            ->first();

        if (!$mapping) {
            return new JsonResponse([
                'object' => 'runtime_resolution',
                'attributes' => [
                    'docker_image' => $image,
                    'resolved' => false,
                    'profile' => 'custom',
                    'message' => 'No mapping found; the daemon will fall back to the custom runtime.',
                ],
            ], JsonResponse::HTTP_OK);
        }

        return new JsonResponse([
            'object' => 'runtime_resolution',
            'attributes' => [
                'docker_image' => $image,
                'resolved' => true,
                'profile' => $mapping->profile->slug,
                'runtime_version' => $mapping->runtime_version,
                'env_path' => $mapping->env_path,
                'extra_env' => $mapping->extra_env,
            ],
        ], JsonResponse::HTTP_OK);
    }

    /**
     * POST /api/application/runtime/mappings
     */
    public function store(StoreRuntimeMappingRequest $request): JsonResponse
    {
        $mapping = RuntimeMapping::query()->create($request->validated());
        $mapping->load('profile');

        return $this->fractal->item($mapping)
            ->transformWith($this->getTransformer(RuntimeMappingTransformer::class))
            ->respond(JsonResponse::HTTP_CREATED);
    }

    /**
     * PATCH /api/application/runtime/mappings/{mapping:id}
     */
    public function update(StoreRuntimeMappingRequest $request, RuntimeMapping $mapping): array
    {
        $mapping->update($request->validated());
        $mapping->refresh();
        $mapping->load('profile');

        return $this->fractal->item($mapping)
            ->transformWith($this->getTransformer(RuntimeMappingTransformer::class))
            ->toArray();
    }

    /**
     * DELETE /api/application/runtime/mappings/{mapping:id}
     */
    public function delete(Request $request, RuntimeMapping $mapping): JsonResponse
    {
        $mapping->delete();

        return new JsonResponse([], JsonResponse::HTTP_NO_CONTENT);
    }
}
