<?php

use Illuminate\Database\Migrations\Migration;
use Illuminate\Database\Schema\Blueprint;
use Illuminate\Support\Facades\Schema;

return new class () extends Migration {
    /**
     * Egg native compatibility classification produced by p:eggs:compat-audit.
     */
    public function up(): void
    {
        Schema::table('eggs', function (Blueprint $table) {
            $table->string('native_compat', 20)->nullable()->after('copy_script_from');
            $table->text('native_notes')->nullable()->after('native_compat');
        });
    }

    public function down(): void
    {
        Schema::table('eggs', function (Blueprint $table) {
            $table->dropColumn(['native_compat', 'native_notes']);
        });
    }
};
