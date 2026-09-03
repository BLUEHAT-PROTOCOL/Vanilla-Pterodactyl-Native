import React, { useEffect, useRef, useState } from 'react';
import { useStoreState } from 'easy-peasy';
import tw from 'twin.macro';

/**
 * Optional background music widget (config/branding.php → music.*).
 *
 * - Browsers routinely block un-muted audio autoplay: we try to start
 *   playback once, and if blocked we wait for the first user interaction
 *   anywhere on the page instead of forcing it.
 * - A small floating widget gives play/pause, mute and a volume slider so
 *   the feature never hijacks the user's speakers on mobile either.
 * - Errors (missing source, codec) simply hide the widget.
 */
export default () => {
    const branding = useStoreState((state) => state.settings.data?.branding?.music);
    const audioRef = useRef<HTMLAudioElement | null>(null);
    const [playing, setPlaying] = useState(false);
    const [muted, setMuted] = useState(false);
    const [volume, setVolume] = useState(branding?.volume ?? 0.5);
    const [failed, setFailed] = useState(false);
    const [ready, setReady] = useState(false);

    useEffect(() => {
        if (!branding?.enabled || !branding.source) return;
        setReady(true);
    }, [branding?.enabled, branding?.source]);

    useEffect(() => {
        if (audioRef.current) {
            audioRef.current.volume = volume;
            audioRef.current.muted = muted;
        }
    }, [volume, muted]);

    const startPlayback = () => {
        const el = audioRef.current;
        if (!el || playing) return;
        el.play()
            .then(() => setPlaying(true))
            .catch(() => setPlaying(false));
    };

    useEffect(() => {
        if (!ready || failed) return;
        // attempt autoplay once; if the browser blocks it, defer to the
        // first user gesture anywhere in the page.
        startPlayback();
        const unlock = () => startPlayback();
        window.addEventListener('pointerdown', unlock, { once: true });
        window.addEventListener('keydown', unlock, { once: true });
        return () => {
            window.removeEventListener('pointerdown', unlock);
            window.removeEventListener('keydown', unlock);
        };
    }, [ready, failed]);

    if (!ready || failed) {
        return null;
    }

    return (
        <div
            css={tw`fixed bottom-3 right-3 z-50 flex items-center gap-2 rounded-lg bg-neutral-800/80 px-3 py-2 text-neutral-100 backdrop-blur-sm`}
        >
            <audio
                ref={audioRef}
                src={branding!.source}
                loop={branding!.loop}
                onError={() => setFailed(true)}
                onEnded={() => setPlaying(false)}
            />
            <button
                type={'button'}
                aria-label={playing ? 'Pause music' : 'Play music'}
                onClick={() => {
                    const el = audioRef.current;
                    if (!el) return;
                    if (playing) {
                        el.pause();
                        setPlaying(false);
                    } else {
                        startPlayback();
                    }
                }}
                css={tw`text-lg leading-none`}
            >
                {playing ? '⏸' : '▶'}
            </button>
            <button
                type={'button'}
                aria-label={muted ? 'Unmute music' : 'Mute music'}
                onClick={() => setMuted((m) => !m)}
                css={tw`text-sm leading-none`}
            >
                {muted || volume === 0 ? '🔇' : '🔊'}
            </button>
            <input
                type={'range'}
                min={0}
                max={1}
                step={0.05}
                value={volume}
                aria-label={'Music volume'}
                onChange={(e) => {
                    const v = Number(e.target.value);
                    setVolume(v);
                    if (v > 0) setMuted(false);
                }}
                css={tw`w-20`}
            />
        </div>
    );
};
