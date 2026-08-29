<?php

namespace Pterodactyl\Transformers\Api\Application;

use Pterodactyl\Models\RuntimeMapping;

class RuntimeMappingTransformer extends BaseTransformer
{
    /**
     * Return the resource name for the transformer.
     */
    public function getResourceName(): string
    {
        return RuntimeMapping::RESOURCE_NAME;
    }

    /**
     * Transform a runtime mapping model.
     */
    public function transform(RuntimeMapping $model): array
    {
        return [
            'id' => $model->id,
            'profile_id' => $model->profile_id,
            'docker_image' => $model->docker_image,
            'runtime_version' => $model->runtime_version,
            'env_path' => $model->env_path,
            'extra_env' => $model->extra_env,
            'created_at' => $model->created_at?->toAtomString(),
            'updated_at' => $model->updated_at?->toAtomString(),
        ];
    }

    /**
     * Include the parent profile.
     */
    public function includeProfile(RuntimeMapping $model): \League\Fractal\Resource\Item
    {
        return $this->item($model->profile, new RuntimeProfileTransformer(), 'runtime_profile');
    }
}
