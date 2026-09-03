<?php

/**
 * Public branding configuration for the Vanilla Pterodactyl Native panel.
 *
 * Everything here is safe to expose to the browser (the AssetComposer ships
 * it to the frontend as part of window.SiteConfiguration). NEVER put secrets
 * in this file.
 *
 * Owner-provided slots (links, media sources) are intentionally EMPTY by
 * default: the panel must not ship invented usernames, URLs or copyrighted
 * media. Fill them in with assets/links you actually own or are licensed to
 * use.
 *
 * - background.type : none | image | video
 * - background.source : URL or a path served under public/ (e.g. /assets/branding/bg.mp4)
 * - background.poster : fallback/poster frame (recommended for video)
 * - background.overlay : dark overlay opacity 0..1 so forms stay readable
 *
 * - music.* : optional background music with visible play/pause + volume
 *   controls; browsers block loud autoplay, the widget handles that gracefully.
 *
 * - dashboard.notice : welcome banner above the server list.
 * - dashboard.links  : array of ['label' => ..., 'url' => ..., 'icon' => optional, 'enabled' => bool]
 */
return [
    'logo_url' => env('BRANDING_LOGO_URL', ''),

    'background' => [
        'type' => env('BRANDING_BG_TYPE', 'none'),
        'source' => env('BRANDING_BG_SOURCE', ''),
        'poster' => env('BRANDING_BG_POSTER', ''),
        'overlay' => (float) env('BRANDING_BG_OVERLAY', 0.45),
    ],

    'music' => [
        'enabled' => (bool) env('BRANDING_MUSIC_ENABLED', false),
        'source' => env('BRANDING_MUSIC_SOURCE', ''),
        'volume' => (float) env('BRANDING_MUSIC_VOLUME', 0.5),
        'loop' => (bool) env('BRANDING_MUSIC_LOOP', true),
    ],

    'dashboard' => [
        'notice' => [
            'enabled' => (bool) env('BRANDING_NOTICE_ENABLED', false),
            'title' => env('BRANDING_NOTICE_TITLE', 'SELAMAT DATANG'),
            'message' => env('BRANDING_NOTICE_MESSAGE', ''),
        ],
        'links' => [],
    ],
];
