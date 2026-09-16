import type { DeviceKind } from '../media/connection'
import type { Words } from './en'
import { plural } from './plural'

/*
ptBR is the interface in Brazilian Portuguese.

A device's name changes the words around it — "seu microfone", "sua câmera" — so
the sentences about devices are written whole for each device rather than built
around its name.
*/

const deviceNames: Record<DeviceKind, string> = {
  audioinput: 'Microfone',
  videoinput: 'Câmera',
  audiooutput: 'Alto-falante',
}

// yours is "your <device>", which agrees with the device.
const yours: Record<DeviceKind, string> = {
  audioinput: 'seu microfone',
  videoinput: 'sua câmera',
  audiooutput: 'seu alto-falante',
}

function capitalized(text: string): string {
  return text.charAt(0).toUpperCase() + text.slice(1)
}

export const ptBR: Words = {
  brand: 'Convia',

  common: {
    loading: 'Carregando…',
    cancel: 'Cancelar',
    save: 'Salvar',
    saving: 'Salvando…',
    remove: 'Remover',
    closed: 'fechada',
    you: 'Você',
    somebody: 'Alguém',
    unnamed: 'Alguém sem nome',
    unreachable: 'Não foi possível falar com a Convia. Tente de novo.',
  },

  refusals: {
    invalid_request: 'A Convia não aceitou isso.',
    malformed_json: 'A Convia não aceitou isso.',
    forbidden: 'Você não tem permissão para fazer isso.',
    conflict: 'Isso não pode ser feito agora.',
    precondition_failed: 'Isso mudou enquanto isso. Tente de novo.',
    rate_limited: 'Tentativas demais. Espere um pouco e tente de novo.',
    payload_too_large: 'Isso é longo demais.',
    internal_error: 'Algo deu errado na Convia. Tente de novo.',
  },

  app: {
    loading: 'Carregando a Convia',
  },

  recovery: {
    pageBroken: 'A Convia parou de funcionar.',
    zoneBroken: 'Esta parte da Convia parou de funcionar.',
    soundBroken: 'O som da chamada parou de funcionar.',
    hint: 'Tentar de novo costuma resolver. Recarregar a página também encerra a chamada em que você está.',
    tryAgain: 'Tentar de novo',
    reload: 'Recarregar a página',
    details: 'Detalhes para relatar',
    copy: 'Copiar detalhes',
    copied: 'Copiado',
    unexpected: 'Algo deu errado. Se a página parar de responder, recarregue-a.',
    dismiss: 'Dispensar',
  },

  signIn: {
    unreachable: 'Não foi possível falar com a Convia. Verifique sua conexão e tente de novo.',
    notAccepted: 'A Convia não aceitou esse nome de usuário ou essa senha. Confira as regras abaixo de cada campo.',
    mismatch: 'Esse nome de usuário e essa senha não correspondem a uma conta.',
    foreignPage: 'Esta página não conseguiu provar que veio da Convia. Recarregue e tente de novo.',
    taken: 'Esse nome de usuário já está em uso. Escolha outro.',
    tooManyAccounts: 'Contas demais foram tentadas daqui. Tente de novo mais tarde.',
    tooManyAttempts: 'Tentativas demais daqui. Espere um pouco e tente de novo.',
    busy: 'A Convia está ocupada agora. Tente de novo em instantes.',
    registerFailed: 'A Convia não conseguiu criar a conta. Tente de novo.',
    signInFailed: 'A Convia não conseguiu fazer sua entrada. Tente de novo.',
    badUsername: 'A Convia não aceita esse nome de usuário. Confira a regra abaixo dele.',
    shortPassword: (minimum) =>
      plural('pt-BR', minimum, {
        one: `A senha precisa ter pelo menos ${minimum} caractere.`,
        other: `A senha precisa ter pelo menos ${minimum} caracteres.`,
      }),
    passwordsDiffer: 'As duas senhas não são iguais.',
    registerTitle: 'Crie sua conta',
    signInTitle: 'Entrar',
    registerLead: 'Sua conta fica nesta Convia, e em nenhum outro lugar.',
    signInLead: 'Converse, reúna-se e mantenha contato.',
    username: 'Nome de usuário',
    usernameRule:
      'De 3 a 32 caracteres: letras, dígitos, pontos, hifens e sublinhados, começando por uma letra ou um dígito.',
    password: 'Senha',
    passwordRule: (minimum) =>
      plural('pt-BR', minimum, {
        one: `Pelo menos ${minimum} caractere. Essa é a única regra.`,
        other: `Pelo menos ${minimum} caracteres. Essa é a única regra.`,
      }),
    confirmation: 'Confirme a senha',
    keepSafe: 'Guarde bem esta senha.',
    keepSafeWhy:
      'Ela tranca a chave da sua conta, então ninguém pode redefini-la, nem quem administra esta Convia. Se você esquecê-la, a conta estará perdida.',
    createAccount: 'Criar conta',
    creatingAccount: 'Criando a conta…',
    signIn: 'Entrar',
    signingIn: 'Entrando…',
    haveAccount: 'Já tenho uma conta',
    wantAccount: 'Criar uma conta',
  },

  presence: {
    online: 'Disponível',
    busy: 'Ocupado',
    away: 'Ausente',
    offline: 'Desconectado',
    yours: 'Seu status',
    current: (state) => `Seu status: ${state}`,
    idle: 'Uma página parada mostra você como ausente.',
  },

  rail: {
    chat: 'Conversas',
    calls: 'Chamadas',
    settings: 'Configurações',
    signOut: 'Sair',
  },

  settings: {
    title: 'Configurações',
    sections: {
      account: 'Conta',
      appearance: 'Aparência',
      language: 'Idioma',
      calls: 'Chamadas',
    },
    back: 'Voltar para as configurações',
    keptHere: 'Guardado neste navegador.',

    handleHeading: 'Seu identificador',
    handleHint: 'Pessoas em qualquer Convia convidam você com ele. Envie-o para quem quiser convidar você.',
    copyHandle: 'Copiar identificador',
    copied: 'Copiado',

    passwordHeading: 'Trocar sua senha',
    currentPassword: 'Senha atual',
    newPassword: 'Nova senha',
    confirmPassword: 'Confirme a nova senha',
    changePassword: 'Trocar senha',
    changingPassword: 'Trocando…',
    passwordChanged: 'Sua senha foi trocada. Todos os outros lugares onde você estava conectado foram desconectados.',
    wrongPassword: 'Essa não é a sua senha atual.',
    tooManyAttempts: 'Tentativas demais daqui. Espere um minuto e tente de novo.',
    passwordNotAccepted: 'A Convia não aceitou essa senha. Confira a regra abaixo dela.',
    passwordFailed: 'Não foi possível trocar a senha. Tente de novo.',

    everywhereHeading: 'Sair de todos os lugares',
    everywhereHint: 'Encerra todas as sessões desta conta, em todos os dispositivos, inclusive neste.',
    everywhere: 'Sair de todos os lugares',
    confirmEverywhere: 'Sair em todos os dispositivos, inclusive neste?',
    signingOut: 'Saindo…',
    stay: 'Continuar conectado',

    theme: 'Tema',
    system: 'Seguir o sistema',
    dark: 'Escuro',
    light: 'Claro',

    languageLegend: 'Idioma da interface',
    browserLanguage: (name) => `O idioma do navegador (${name})`,

    callsHint: 'Como as chamadas começam, e quais dispositivos usam.',
    startMicrophone: 'Começar chamadas com o microfone ligado',
    startCamera: 'Começar chamadas com a câmera ligada',
    checkDevices: 'Testar meus dispositivos',
    stopChecking: 'Parar o teste',
    checking: 'Testando seus dispositivos',
    inCall: 'Você está em uma chamada, então os dispositivos dela não podem ser testados aqui. O que você escolher vale para a chamada na hora.',
  },

  workspace: {
    skip: 'Pular para a conversa',
    skipToSettings: 'Pular para as configurações',
    conversations: 'Conversas',
    retrying: 'Não foi possível falar com a Convia. Tentando de novo.',
    nothingOpen: 'Nada está aberto.',
    pickOne: 'Escolha uma conversa à esquerda, ou abra uma nova.',
    aRoom: 'uma sala',
    roomDeleted: (room) => `${room} foi apagada.`,
  },

  sidebar: {
    heading: 'Conversas',
    cannotOpen: 'Você não pode abrir salas agora.',
    openFailed: 'Não foi possível abrir a sala.',
    notALink: 'Esse não é um link de convite da Convia.',
    unusableLink: 'Esse convite não pode ser usado. Ele pode ter expirado, já ter sido usado ou ser para outra pessoa.',
    alreadyIn: 'Você já está nessa sala.',
    homeUnreachable: 'Não foi possível falar com a Convia desse link. Tente de novo em instantes.',
    linkFailed: 'Não foi possível usar esse link. Tente de novo.',
    nameLabel: 'Nome da conversa',
    namePlaceholder: 'Dê um nome',
    opening: 'Abrindo…',
    create: 'Criar',
    linkLabel: 'Link de convite',
    linkPlaceholder: 'Cole o link que você recebeu',
    looking: 'Consultando…',
    look: 'Consultar',
    invitedBy: (inviter, host) => `Convite de ${inviter}, em ${host}.`,
    joining: 'Entrando…',
    join: 'Entrar',
    joinWithLink: 'Entrar com um link',
    newConversation: 'Nova conversa',
    empty: 'Você ainda não está em nenhuma conversa. Abra uma, ou entre em uma com um link que alguém lhe enviou.',
    unread: (count) =>
      plural('pt-BR', count, { one: `${count} mensagem não lida`, other: `${count} mensagens não lidas` }),
    manyUnread: '99+',
    roomClosed: 'Esta sala está fechada',
    elsewhereLabel: 'Salas em outras Convias',
    elsewhere: 'Em outros lugares',
  },

  conversation: {
    removedByOwner: 'Removida pelo dono da sala.',
    withdrawn: 'Esta mensagem foi retirada.',
    editLabel: 'Editar mensagem',
    edited: '(editada)',
    editedAt: (at) => `Editada em ${at}`,
    edit: 'Editar',
    withdraw: 'Retirar',
    speaker: (name) => `${name}: `,
    guest: 'Um convidado',
    somebodyWhoLeft: 'Alguém que saiu',
    back: 'Voltar para as conversas',
    people: 'Pessoas',
    unreadable: 'Não foi possível ler esta conversa. A Convia vai tentar de novo.',
    empty: 'Nada foi dito aqui ainda.',
  },

  composer: {
    unreachable: 'Não foi enviada: não foi possível falar com a Convia.',
    closed: 'Esta sala está fechada. Nada mais pode ser dito aqui.',
    gone: 'Esta sala não está mais disponível para você.',
    failed: 'Não foi enviada.',
    label: (room) => `Mensagem para ${room}`,
    closedPlaceholder: 'Esta sala está fechada',
    send: 'Enviar',
  },

  people: {
    label: (room) => `Pessoas em ${room}`,
    livesOn: (host) => `Esta sala fica em ${host}. Você participa pela sua própria Convia.`,
    inRoom: 'Nesta sala',
    membersUnreadable: 'Não foi possível ler quem está aqui.',
    owner: 'dono',
    youTag: '(você)',
    remove: (name) => `Remover ${name}`,
    removeFailed: (name) => `Não foi possível remover ${name}.`,
    ban: 'Banir',
    banNamed: (name) => `Banir ${name}`,
    banFailed: (name) => `Não foi possível banir ${name}.`,
    addHeading: 'Adicionar alguém',
    candidatesUnreadable: 'Não foi possível ler quem você poderia adicionar.',
    nobodyShared: 'Você ainda não compartilha uma sala com mais ninguém.',
    everybodyHere: 'Todos com quem você compartilha uma sala já estão aqui.',
    add: 'Adicionar',
    addNamed: (name) => `Adicionar ${name}`,
    adding: 'Adicionando…',
    cannotAdd: (name) => `Não é possível adicionar ${name} a esta sala.`,
    addHint: 'Você pode adicionar pessoas com quem já compartilha uma sala.',
    banned: 'Banidos',
    bansUnreadable: 'Não foi possível ler quem está banido.',
    nobodyBanned: 'Ninguém está banido desta sala.',
    unban: 'Desbanir',
    unbanNamed: (name) => `Desbanir ${name}`,
    unbanFailed: (name) => `Não foi possível desbanir ${name}.`,
    bansHint:
      'Ninguém pode adicionar ou convidar alguém banido até você suspender o banimento. Quem você apenas remove pode ser adicionado de volta.',
    inviteHeading: 'Convidar pelo identificador',
    handle: 'Identificador',
    handlePlaceholder: 'nome#IDENTIFICADOR',
    inviting: 'Convidando…',
    invite: 'Convidar',
    badHandle: 'Esse identificador não está certo. Confira com a pessoa: cada caractere conta.',
    noLongerIn: 'Você não está mais nesta sala.',
    alreadyIn: 'Essa pessoa já está nesta sala.',
    inviteFailed: 'Não foi possível fazer o convite. Tente de novo.',
    sendLink: (invitee) => `Envie este link para ${invitee}. Ele funciona uma vez, por um dia, e só para essa pessoa.`,
    linkLabel: 'Link de convite',
    copied: 'Copiado',
    copy: 'Copiar link',
    thisComputer:
      'Este link aponta para este computador, então só funciona para quem usa a Convia nesta mesma máquina. Abra a Convia num endereço que outras pessoas alcancem para convidá-las.',
    anyConvia: 'Pessoas em qualquer Convia podem entrar com o identificador delas. Ele aparece nas configurações.',
    pendingHeading: 'Aguardando entrar',
    pendingUnreadable: 'Não foi possível ler os convites que você fez.',
    until: (time) => `até ${time}`,
    copyFor: (invitee) => `Copiar o link para ${invitee}`,
    withdraw: 'Retirar',
    withdrawFor: (invitee) => `Retirar o convite para ${invitee}`,
    withdrawFailed: 'Não foi possível retirar o convite. Tente de novo.',
    notConfirmed:
      'A Convia onde esta sala fica não confirmou sua saída, então você continua nela. Tente de novo mais tarde.',
    forgetFailed: 'Não foi possível esquecer esta sala. Tente de novo.',
    forgetInstead: (host) =>
      `Você pode esquecê-la aqui. Ela sai da sua lista, mas ${host} continua contando você como membro, e nada aqui poderá tirar você de lá depois.`,
    forgetting: 'Esquecendo…',
    forget: 'Esquecer aqui',
    confirmLeave: (room) => `Sair de ${room}? O que você disse continua lá.`,
    leaving: 'Saindo…',
    leave: 'Sair',
    stay: 'Ficar',
    leaveRoom: 'Sair desta sala',
  },

  roomSettings: {
    open: 'Sala',
    label: (room) => `Configurações de ${room}`,
    failed: 'Não foi possível fazer isso. Tente de novo.',
    name: 'Nome da sala',
    confirmDelete: (room) => `Apagar ${room} para todos que estão nela?`,
    deleting: 'Apagando…',
    delete: 'Apagar',
    keep: 'Manter',
    rename: 'Renomear',
    reopen: 'Reabrir',
    close: 'Fechar',
    deleteRoom: 'Apagar sala',
  },

  call: {
    start: 'Iniciar chamada',
    join: 'Entrar na chamada',
    cannotStart: 'Uma sala fechada não inicia novas chamadas.',
    dismiss: 'Dispensar',

    refused: (refusal, kind) => {
      const device = yours[kind]
      switch (refusal) {
        case 'denied':
          return `A Convia não tem permissão para usar ${device}. Permita nas configurações do site, ao lado da barra de endereço, e tente de novo.`
        case 'missing':
          return kind === 'audioinput' ? 'Nenhum microfone foi encontrado.' : 'Nenhuma câmera foi encontrada.'
        case 'busy':
          return `${capitalized(device)} está sendo usad${kind === 'videoinput' ? 'a' : 'o'} por outro aplicativo.`
        case 'failed':
          return `Não foi possível ligar ${device}.`
      }
    },
    failed: (refusal, kind) => {
      const device = kind === undefined ? 'seu microfone ou sua câmera' : yours[kind]
      const feminine = kind === 'videoinput'
      switch (refusal) {
        case 'denied':
          return `A Convia não tem mais permissão para usar ${device}.`
        case 'missing':
          return kind === undefined
            ? 'Seu microfone ou sua câmera foi desconectado.'
            : `${capitalized(device)} foi desconectad${feminine ? 'a' : 'o'}.`
        case 'busy':
          return kind === undefined
            ? 'Seu microfone ou sua câmera está sendo usado por outro aplicativo.'
            : `${capitalized(device)} está sendo usad${feminine ? 'a' : 'o'} por outro aplicativo.`
        case 'failed':
          return `${capitalized(device)} parou de funcionar.`
      }
    },
    replaced: (kind) =>
      `${capitalized(yours[kind])} foi desconectad${kind === 'videoinput' ? 'a' : 'o'}, então a chamada está usando o padrão do sistema.`,
    unheard: 'Seu microfone não está disponível, então ninguém consegue ouvir você.',
    unseen: 'Sua câmera não está disponível, então ninguém consegue ver você.',
    microphoneUnavailable: 'Seu microfone não está disponível.',
    cameraUnavailable: 'Sua câmera não está disponível.',
    deviceUnusable: 'Não foi possível usar esse dispositivo.',

    device: (kind) => deviceNames[kind],
    numbered: (kind, position) => `${deviceNames[kind]} ${position}`,
    systemDefault: 'Padrão do sistema',
    level: 'Nível do microfone',

    prepare: 'Preparar para entrar',
    startingDevices: 'Ligando seus dispositivos…',
    cameraOff: 'Sua câmera está desligada',
    microphoneOn: 'Ligar microfone',
    microphoneOff: 'Desligar microfone',
    cameraOn: 'Ligar câmera',
    cameraOffAction: 'Desligar câmera',
    tryAgain: 'Tentar de novo',

    weakConnection: 'conexão fraca',
    lostConnection: 'conexão perdida',
    muted: 'sem som',
    takeOut: (name) => `Tirar ${name} da chamada`,

    stage: 'Chamada',
    joiningCall: 'Entrando na chamada…',
    reconnecting: 'Reconectando à chamada…',
    weak: 'Sua conexão está fraca. Os outros podem não ouvir ou ver você bem.',
    audioOnly: 'Só áudio: o vídeo está pausado para poupar sua conexão.',
    resumeVideo: 'Religar o vídeo',
    weakForAWhile: 'Sua conexão está fraca há algum tempo.',
    goAudioOnly: 'Continuar só com áudio',
    notNow: 'Agora não',
    people: 'Pessoas na chamada',
    joiningPerson: 'Entrando…',
    controls: 'Controles da chamada',
    mute: 'Silenciar',
    unmute: 'Ativar som',
    startCamera: 'Ligar câmera',
    stopCamera: 'Desligar câmera',
    devices: 'Dispositivos',
    leave: 'Sair da chamada',

    notices: 'Avisos da chamada',
    joined: (name) => `${name} entrou na chamada.`,
    left: (name) => `${name} saiu da chamada.`,

    current: 'Chamada atual',
    joiningIn: 'Entrando na chamada em ',
    inCallIn: 'Em uma chamada em ',
    reconnectingShort: ' — reconectando…',
    return: 'Voltar',

    list: 'Chamadas',
    noneRunning: 'Nenhuma chamada está acontecendo nas suas salas. Inicie uma a partir de uma conversa.',
    since: (time) => `desde ${time}`,

    forbidden: 'Você não pode entrar nesta chamada. Talvez tenham tirado você dela.',
    roomGone: 'Esta sala não está mais disponível.',
    cannotJoin: 'Não é possível entrar nesta chamada agora. Uma sala fechada não inicia novas chamadas.',
    noCalls: 'Chamadas não podem acontecer aqui agora.',
    joinFailed: 'Não foi possível entrar na chamada. Tente de novo.',
    notOwner: 'Só o dono da sala pode tirar alguém da chamada.',
    notInCall: 'Essa pessoa não está mais na chamada.',
    joinFirst: 'Entre na chamada para tirar alguém dela.',
    removeFailed: 'Não foi possível tirar essa pessoa da chamada.',
    removed: 'Tiraram você da chamada.',
    ended: 'A chamada terminou.',
    elsewhere: 'Você entrou nesta chamada em outro lugar, então ela foi fechada aqui.',
    lost: 'A conexão com a chamada caiu.',
    mediaUnreachable: 'Não foi possível falar com o servidor de mídia da chamada.',
  },
}
