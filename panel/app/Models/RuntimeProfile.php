<?php

namespace Pterodactyl\Models;

use Illuminate\Database\Eloquent\Relations\HasMany;

/**
 * Pterodactyl\Models\RuntimeProfile.
 *
 * @property int $id
 * @property string $uuid
 * @property string $name
 * @property string $slug
 * @property string|null $binary
 * @property string|null $binary_args
 * @property string|null $description
 * @property array|null $supported_versions
 * @property string|null $default_image
 * @property string|null $health_check
 * @property \Illuminate\Support\Carbon $created_at
 * @property \Illuminate\Support\Carbon $updated_at
 * @property \Illuminate\Support\Collection $mappings
 *
 * @mixin \Eloquent
 */
class RuntimeProfile extends Model
{
    public const RESOURCE_NAME = 'runtime_profile';

    public const PROFILE_NODE = 'node';
    public const PROFILE_PYTHON = 'python';
    public const PROFILE_JAVA = 'java';
    public const PROFILE_STATIC = 'static';
    public const PROFILE_CUSTOM = 'custom';

    /**
     * Fields users can search against.
     */
    public static array $searchableColumns = ['name', 'slug', 'description'];

    protected $table = 'runtime_profiles';

    protected $fillable = [
        'uuid',
        'name',
        'slug',
        'binary',
        'binary_args',
        'description',
        'supported_versions',
        'default_image',
        'health_check',
    ];

    protected $casts = [
        'supported_versions' => 'array',
    ];

    public function getRouteKeyName(): string
    {
        return 'uuid';
    }

    public function mappings(): HasMany
    {
        return $this->hasMany(RuntimeMapping::class);
    }
}
