<?php

namespace Pterodactyl\Http\Requests\Api\Application\Runtime;

use Pterodactyl\Services\Acl\Api\AdminAcl;
use Pterodactyl\Http\Requests\Api\Application\ApplicationApiRequest;

class StoreRuntimeProfileRequest extends ApplicationApiRequest
{
    protected ?string $resource = AdminAcl::RESOURCE_RUNTIME;

    protected int $permission = AdminAcl::WRITE;

    /**
     * Validation rules.
     */
    public function rules(): array
    {
        return [
            'name' => 'required|string|max:120',
            'slug' => 'required|string|max:60|alpha_dash|unique:runtime_profiles,slug',
            'binary' => 'nullable|string|max:120',
            'binary_args' => 'nullable|string|max:255',
            'description' => 'nullable|string|max:500',
            'supported_versions' => 'nullable|array',
            'default_image' => 'nullable|string|max:255',
            'health_check' => 'nullable|string|max:120',
        ];
    }
}
