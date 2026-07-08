import React from 'react';
import {Composition} from 'remotion';
import {Main} from './Main';

// 7 scenes minus 6×14-frame transition overlaps.
const DURATION = 150 + 220 + 430 + 300 + 290 + 200 + 180 - 6 * 14;

export const Root: React.FC = () => (
  <Composition
    id="TokenRouter"
    component={Main}
    durationInFrames={DURATION}
    fps={30}
    width={1920}
    height={1080}
  />
);
