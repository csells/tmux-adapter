import { TmuxConverterWeb } from './tmux-converter-web.js';
export { TmuxConverterWeb } from './tmux-converter-web.js';
export { encodeBinaryFrame, BinaryMsgType } from '../shared/protocol.js';

customElements.define('tmux-converter-web', TmuxConverterWeb);
