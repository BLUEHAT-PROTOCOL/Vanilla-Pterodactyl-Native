<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class () extends Migration {
    /**
     * Native runtime profiles: a named runtime (node, python, java, static, custom)
     * with its binary, default versions and supported version list.
     */
    public function up(): void
    {
        Schema::create('runtime_profiles', function (Blueprint $table) {
            $table->id();
            $table->uuid('uuid')->unique();
            $table->string('name');
            $table->string('slug')->unique();
            $table->string('binary')->nullable();
            $table->string('binary_args')->nullable();
            $table->string('description')->nullable();
            $table->json('supported_versions')->nullable();
            $table->string('default_image')->nullable();
            $table->string('health_check')->nullable();
            $table->timestamps();
        });

        Schema::create('runtime_mappings', function (Blueprint $table) {
            $table->id();
            $table->unsignedBigInteger('profile_id');
            $table->string('docker_image')->unique();
            $table->string('runtime_version')->nullable();
            $table->string('env_path')->nullable();
            $table->json('extra_env')->nullable();
            $table->timestamps();

            $table->foreign('profile_id')->references('id')->on('runtime_profiles')->onDelete('cascade');
        });
    }

    public function down(): void
    {
        Schema::dropIfExists('runtime_mappings');
        Schema::dropIfExists('runtime_profiles');
    }
};
