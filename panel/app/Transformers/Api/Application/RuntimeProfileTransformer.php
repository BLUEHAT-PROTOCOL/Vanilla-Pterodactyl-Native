<?php

namespace Pterodactyl\Transformers\Api\Application;

use Pterodactyl\Models\RuntimeProfile;

class RuntimeProfileTransformer extends BaseTransformer
{
    /**
     * Return the resource name for the transformer.
     */
    public function getResourceName(): string
    {
        return RuntimeProfile::RESOURCE_NAME;
    }

    /**
     * Transform a runtime profile model.
     */
    public function transform(RuntimeProfile $model): array
    {
        return [
            'uuid' => $model->uuid,
            'name' => $model->name,
            'slug' => $model->slug,
            'binary' => $model->binary,
            'binary_args' => $model->binary_args,
            'description' => $model->description,
            'supported_versions' => $model->supported_versions,
            'default_image' => $model->default_image,
            'health_check' => $model->health_check,
            'created_at' => $model->created_at?->toAtomString(),
            'updated_at' => $model->updated_at?->toAtomString(),
        ];
    }

    /**
     * Include the runtime mappings for this profile.
     */
    public function includeMappings(RuntimeProfile $model): \League\Fractal\Resource\Collection
    {
        return $this->collection($model->mappings, new RuntimeMappingTransformer(), 'runtime_mapping');
    }
}
