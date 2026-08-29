<?php

namespace Pterodactyl\Http\Requests\Api\Application\Runtime;

use Pterodactyl\Services\Acl\Api\AdminAcl;
use Pterodactyl\Http\Requests\Api\Application\ApplicationApiRequest;

class StoreRuntimeMappingRequest extends ApplicationApiRequest
{
    protected ?string $resource = AdminAcl::RESOURCE_RUNTIME;

    protected int $permission = AdminAcl::WRITE;

    /**
     * Validation rules.
     */
    public function rules(): array
    {
        return [
            'profile_id' => 'required|integer|exists:runtime_profiles,id',
            'docker_image' => 'required|string|max:255|unique:runtime_mappings,docker_image',
            'runtime_version' => 'nullable|string|max:60',
            'env_path' => 'nullable|string|max:255',
            'extra_env' => 'nullable|array',
        ];
    }
}
