<?php

namespace Pterodactyl\Models;

use Illuminate\Database\Eloquent\Relations\BelongsTo;

/**
 * Pterodactyl\Models\RuntimeMapping.
 *
 * @property int $id
 * @property int $profile_id
 * @property string $docker_image
 * @property string|null $runtime_version
 * @property string|null $env_path
 * @property array|null $extra_env
 * @property \Illuminate\Support\Carbon $created_at
 * @property \Illuminate\Support\Carbon $updated_at
 * @property RuntimeProfile $profile
 *
 * @mixin \Eloquent
 */
class RuntimeMapping extends Model
{
    public const RESOURCE_NAME = 'runtime_mapping';

    protected $table = 'runtime_mappings';

    protected $fillable = [
        'profile_id',
        'docker_image',
        'runtime_version',
        'env_path',
        'extra_env',
    ];

    protected $casts = [
        'profile_id' => 'integer',
        'extra_env' => 'array',
    ];

    public function profile(): BelongsTo
    {
        return $this->belongsTo(RuntimeProfile::class);
    }
}
