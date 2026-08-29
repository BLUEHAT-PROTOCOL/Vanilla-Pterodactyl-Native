<?php

namespace Pterodactyl\Http\Requests\Api\Application\Runtime;

use Pterodactyl\Services\Acl\Api\AdminAcl;
use Pterodactyl\Http\Requests\Api\Application\ApplicationApiRequest;

class UpdateRuntimeProfileRequest extends ApplicationApiRequest
{
    protected ?string $resource = AdminAcl::RESOURCE_RUNTIME;

    protected int $permission = AdminAcl::WRITE;

    /**
     * Validation rules (slug uniqueness enforced ignoring the current model).
     */
    public function rules(): array
    {
        /** @var \Pterodactyl\Models\RuntimeProfile|null $profile */
        $profile = $this->route()->parameter('profile');

        return [
            'name' => 'sometimes|string|max:120',
            'slug' => 'sometimes|string|max:60|alpha_dash' . ($profile ? '|unique:runtime_profiles,slug,' . $profile->id : ''),
            'binary' => 'nullable|string|max:120',
            'binary_args' => 'nullable|string|max:255',
            'description' => 'nullable|string|max:500',
            'supported_versions' => 'nullable|array',
            'default_image' => 'nullable|string|max:255',
            'health_check' => 'nullable|string|max:120',
        ];
    }
}
