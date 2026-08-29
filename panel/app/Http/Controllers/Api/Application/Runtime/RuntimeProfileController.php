<?php

namespace Pterodactyl\Http\Controllers\Api\Application\Runtime;

use Illuminate\Http\JsonResponse;
use Illuminate\Http\Request;
use Ramsey\Uuid\Uuid;
use Pterodactyl\Http\Requests\Api\Application\Runtime\StoreRuntimeProfileRequest;
use Pterodactyl\Http\Requests\Api\Application\Runtime\UpdateRuntimeProfileRequest;
use Pterodactyl\Models\RuntimeProfile;
use Pterodactyl\Http\Controllers\Api\Application\ApplicationApiController;
use Pterodactyl\Transformers\Api\Application\RuntimeProfileTransformer;

class RuntimeProfileController extends ApplicationApiController
{
    /**
     * GET /api/application/runtime/profiles
     */
    public function index(Request $request): array
    {
        $profiles = RuntimeProfile::query()->with('mappings')->paginate($request->query('per_page') ?? 50);

        return $this->fractal->collection($profiles)
            ->transformWith($this->getTransformer(RuntimeProfileTransformer::class))
            ->toArray();
    }

    /**
     * GET /api/application/runtime/profiles/{uuid}
     */
    public function view(Request $request, RuntimeProfile $profile): array
    {
        $profile->load('mappings');

        return $this->fractal->item($profile)
            ->transformWith($this->getTransformer(RuntimeProfileTransformer::class))
            ->toArray();
    }

    /**
     * POST /api/application/runtime/profiles
     */
    public function store(StoreRuntimeProfileRequest $request): JsonResponse
    {
        $data = $request->validated();
        $data['uuid'] = Uuid::uuid4()->toString();

        $profile = RuntimeProfile::query()->create($data);

        return $this->fractal->item($profile->fresh())
            ->transformWith($this->getTransformer(RuntimeProfileTransformer::class))
            ->respond(JsonResponse::HTTP_CREATED);
    }

    /**
     * PATCH /api/application/runtime/profiles/{uuid}
     */
    public function update(UpdateRuntimeProfileRequest $request, RuntimeProfile $profile): array
    {
        $profile->update($request->validated());
        $profile->refresh();
        $profile->load('mappings');

        return $this->fractal->item($profile)
            ->transformWith($this->getTransformer(RuntimeProfileTransformer::class))
            ->toArray();
    }

    /**
     * DELETE /api/application/runtime/profiles/{uuid}
     */
    public function delete(Request $request, RuntimeProfile $profile): JsonResponse
    {
        $profile->delete();

        return new JsonResponse([], JsonResponse::HTTP_NO_CONTENT);
    }
}
