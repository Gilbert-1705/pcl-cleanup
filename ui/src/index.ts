import { defineModule } from '@prusa/connect-ui/slots';

import { CleanupPage } from './cleanup-page';
import { holdHost } from './host';
import './styles.css';

export default defineModule((host) => {
  holdHost(host);

  host.register.page('cleanup', CleanupPage);
});
